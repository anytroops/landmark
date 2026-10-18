package coordinator

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/cluster"
	"github.com/anytroops/landmark/internal/segment"
	"github.com/anytroops/landmark/internal/shard"
	"github.com/anytroops/landmark/internal/stoplist"
)

// The test index: 6 global tracks over 2 partitions (partition = id mod 2).
// Track d holds 50 hashes, d*1000 .. d*1000+49, at t = 0..49. Track 5 is an
// exact duplicate of track 2 (same hashes) living in the other partition,
// and the catalog puts them in one cluster.
const (
	nTracks     = 6
	nPartitions = 2
	perTrack    = 50
	dupOf2      = 5
	deadline    = 500 * time.Millisecond
)

func trackHashBase(d uint32) uint32 {
	if d == dupOf2 {
		return 2 * 1000
	}
	return d * 1000
}

// query returns landmarks for track d, shifted so the true offset is 5.
func query(d uint32) *landmarkv1.IdentifyRequest {
	req := &landmarkv1.IdentifyRequest{}
	for i := uint32(5); i < perTrack; i++ {
		req.Hashes = append(req.Hashes, trackHashBase(d)+i)
		req.Times = append(req.Times, i-5)
	}
	return req
}

type mode int32

const (
	normal mode = iota
	hang
	fail
	overloaded
	wrongPartition
	wrongCRC
)

// testShard wraps a real shard.Server with injectable faults.
type testShard struct {
	landmarkv1.UnimplementedShardServiceServer
	real     *shard.Server
	mode     atomic.Int32
	searches atomic.Int64
	last     atomic.Pointer[landmarkv1.SearchRequest] // most recent request
}

func (s *testShard) set(m mode) { s.mode.Store(int32(m)) }

func (s *testShard) Search(ctx context.Context, req *landmarkv1.SearchRequest) (*landmarkv1.SearchResponse, error) {
	s.searches.Add(1)
	s.last.Store(req)
	switch mode(s.mode.Load()) {
	case hang:
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	case fail:
		return nil, status.Error(codes.Unavailable, "injected failure")
	case overloaded:
		return nil, status.Error(codes.ResourceExhausted, "injected overload")
	}
	resp, err := s.real.Search(ctx, req)
	if err == nil && mode(s.mode.Load()) == wrongPartition {
		resp.PartitionId ^= 1
	}
	return resp, err
}

func (s *testShard) Info(ctx context.Context, req *landmarkv1.InfoRequest) (*landmarkv1.InfoResponse, error) {
	info, err := s.real.Info(ctx, req)
	if err == nil && mode(s.mode.Load()) == wrongCRC {
		info.SegmentCrc32C ^= 1
	}
	return info, err
}

// testReplica is a shard server on a real TCP port that can be killed and restarted.
type testReplica struct {
	t      *testing.T
	addr   string
	shard  *testShard
	health *health.Server
	mu     sync.Mutex
	gs     *grpc.Server
}

func (r *testReplica) start() {
	r.t.Helper()
	lis, err := net.Listen("tcp", r.addr)
	if err != nil {
		r.t.Fatalf("listen %s: %v", r.addr, err)
	}
	r.addr = lis.Addr().String()
	gs := grpc.NewServer()
	landmarkv1.RegisterShardServiceServer(gs, r.shard)
	healthpb.RegisterHealthServer(gs, r.health)
	r.mu.Lock()
	r.gs = gs
	r.mu.Unlock()
	go func() { _ = gs.Serve(lis) }()
}

// kill stops the server abruptly, as a crashed process would.
func (r *testReplica) kill() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gs.Stop()
}

type testCluster struct {
	reps  [nPartitions][2]*testReplica
	coord *Coordinator
}

func newTestCluster(t *testing.T, tRatio float64, opts Options) *testCluster {
	t.Helper()
	return newTestClusterWith(t, tRatio, opts, nil)
}

// newTestClusterWith lets configure change the cluster before the
// coordinator starts.
func newTestClusterWith(t *testing.T, tRatio float64, opts Options, configure func(*cluster.Cluster)) *testCluster {
	t.Helper()
	dir := t.TempDir()
	tc := &testCluster{}
	crcs := make([]uint32, nPartitions)
	for p := range uint32(nPartitions) {
		var entries []segment.Entry
		for d := p; d < nTracks; d += nPartitions {
			for i := range uint32(perTrack) {
				entries = append(entries, segment.Entry{Hash: trackHashBase(d) + i, Track: d / nPartitions, T: i})
			}
		}
		path := filepath.Join(dir, "p"+string(rune('0'+p))+".lmrk")
		meta := segment.Meta{Partition: p, NPartitions: nPartitions, NTracks: nTracks / nPartitions, Encoding: segment.EncodingVarint}
		if err := segment.Write(path, meta, entries); err != nil {
			t.Fatal(err)
		}
		seg, err := segment.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = seg.Close() })
		crcs[p] = seg.Checksum()
		for k := range 2 {
			srv, err := shard.New(seg, shard.Config{Workers: 4, MaxQueueWait: time.Second}, prometheus.NewRegistry())
			if err != nil {
				t.Fatal(err)
			}
			hs := health.NewServer()
			hs.SetServingStatus(landmarkv1.ShardService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
			r := &testReplica{t: t, addr: "127.0.0.1:0", shard: &testShard{real: srv}, health: hs}
			r.start()
			t.Cleanup(r.kill)
			tc.reps[p][k] = r
		}
	}

	catalog := make([]cluster.Track, nTracks)
	for d := range uint32(nTracks) {
		catalog[d] = cluster.Track{Dense: d, Partition: d % nPartitions, Cluster: d}
	}
	catalog[dupOf2].Cluster = 2
	cl := &cluster.Cluster{
		Config: cluster.Config{
			DeadlineMs: int(deadline.Milliseconds()), TAbs: 10, TRatio: tRatio,
			Partitions: []cluster.Partition{
				{ID: 0, Replicas: []string{tc.reps[0][0].addr, tc.reps[0][1].addr}},
				{ID: 1, Replicas: []string{tc.reps[1][0].addr, tc.reps[1][1].addr}},
			},
		},
		ExpectedCRC: crcs,
		Catalog:     catalog,
	}
	if configure != nil {
		configure(cl)
	}
	if opts.HealthInterval == 0 {
		opts.HealthInterval = 50 * time.Millisecond
	}
	if opts.EjectFor == 0 {
		opts.EjectFor = 200 * time.Millisecond
	}
	coord, err := New(cl, opts, prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	coord.Start(ctx)
	t.Cleanup(func() { cancel(); _ = coord.Close() })
	tc.coord = coord
	return tc
}

func (tc *testCluster) identify(t *testing.T, d uint32) *landmarkv1.IdentifyResponse {
	t.Helper()
	resp, err := tc.coord.Identify(context.Background(), query(d))
	if err != nil {
		t.Fatalf("Identify(track %d): %v", d, err)
	}
	return resp
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestIdentifyMergesAcrossPartitions(t *testing.T) {
	tc := newTestCluster(t, 1.0, Options{})
	for d := range uint32(nTracks) {
		resp := tc.identify(t, d)
		if resp.GetPartial() || resp.GetPartitionsResponded() != 2 || resp.GetPartitionsTotal() != 2 {
			t.Fatalf("track %d: partial=%v responded %d/%d", d, resp.GetPartial(), resp.GetPartitionsResponded(), resp.GetPartitionsTotal())
		}
		top := resp.GetCandidates()[0]
		want := d
		if d == dupOf2 {
			want = 2 // identical score; ties go to the lower track id
		}
		if top.GetTrackId() != want || top.GetScore() != perTrack-5 || top.GetOffset() != 5 || !resp.GetMatched() {
			t.Errorf("track %d: top = %v, matched=%v", d, top, resp.GetMatched())
		}
	}
}

func TestDuplicateAcrossPartitionsDoesNotBlockAcceptance(t *testing.T) {
	// t_ratio 2: track 2's twin (track 5, other partition) ties its score. The
	// runner-up must skip the twin's cluster, or the match is rejected (D14).
	tc := newTestCluster(t, 2.0, Options{})
	resp := tc.identify(t, 2)
	c := resp.GetCandidates()
	if len(c) < 2 || c[0].GetScore() != c[1].GetScore() || c[1].GetTrackId() != dupOf2 {
		t.Fatalf("expected tied twins on top, got %v", c)
	}
	if !resp.GetMatched() {
		t.Error("match rejected: duplicate counted as the runner-up")
	}
}

func TestKilledReplicaStillGivesCompleteAnswers(t *testing.T) {
	tc := newTestCluster(t, 1.0, Options{})
	tc.reps[1][0].kill()
	for range 20 {
		resp := tc.identify(t, 3)
		if resp.GetPartial() || !resp.GetMatched() || resp.GetCandidates()[0].GetTrackId() != 3 {
			t.Fatalf("with one replica dead: %v", resp)
		}
	}
}

func TestWholePartitionDownIsPartialNotWrong(t *testing.T) {
	tc := newTestCluster(t, 1.0, Options{})
	tc.reps[1][0].kill()
	tc.reps[1][1].kill()

	// Track 2 lives in the surviving partition: still found, flagged partial.
	resp := tc.identify(t, 2)
	if !resp.GetPartial() || resp.GetPartitionsResponded() != 1 || !resp.GetMatched() {
		t.Fatalf("track 2: %v", resp)
	}
	// Track 3 lives in the dead partition: no match, never a wrong one.
	resp = tc.identify(t, 3)
	if !resp.GetPartial() || resp.GetMatched() {
		t.Fatalf("track 3 with its partition down should be partial and unmatched: %v", resp)
	}
}

func TestAllPartitionsDownIsUnavailable(t *testing.T) {
	tc := newTestCluster(t, 1.0, Options{})
	for p := range nPartitions {
		tc.reps[p][0].kill()
		tc.reps[p][1].kill()
	}
	_, err := tc.coord.Identify(context.Background(), query(0))
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %v, want Unavailable", status.Code(err))
	}
}

func TestHungReplicaTimesOutAndRetries(t *testing.T) {
	tc := newTestCluster(t, 1.0, Options{EjectAfter: 1000}) // keep it in rotation
	tc.reps[1][0].shard.set(hang)
	for i := range 20 {
		start := time.Now()
		resp := tc.identify(t, 3)
		if resp.GetPartial() || resp.GetCandidates()[0].GetTrackId() != 3 {
			t.Fatalf("query %d: %v", i, resp)
		}
		if el := time.Since(start); el > deadline {
			t.Fatalf("query %d took %v, over the %v deadline", i, el, deadline)
		}
	}
	if tc.reps[1][0].shard.searches.Load() == 0 {
		t.Fatal("hung replica never tried; test proves nothing")
	}
	if testutil.ToFloat64(tc.coord.metrics.retries) == 0 {
		t.Error("no retries recorded")
	}
}

func TestFailingReplicaIsEjectedThenReinstated(t *testing.T) {
	tc := newTestCluster(t, 1.0, Options{EjectAfter: 3})
	bad := tc.reps[1][0]
	r := tc.coord.parts[1][0]
	bad.shard.set(fail)
	waitFor(t, "ejection", func() bool {
		tc.identify(t, 3) // every answer stays complete via the other replica
		return testutil.ToFloat64(tc.coord.metrics.ejections.WithLabelValues(r.addr)) == 1
	})
	before := bad.shard.searches.Load()
	for range 10 {
		tc.identify(t, 3)
	}
	if got := bad.shard.searches.Load() - before; got != 0 {
		t.Errorf("ejected replica still received %d searches", got)
	}

	bad.shard.set(normal)
	waitFor(t, "reinstatement", r.available)
}

func TestOverloadedReplicaIsNotEjected(t *testing.T) {
	tc := newTestCluster(t, 1.0, Options{EjectAfter: 2})
	tc.reps[1][0].shard.set(overloaded)
	for range 30 {
		if resp := tc.identify(t, 3); resp.GetPartial() {
			t.Fatal("overload on one replica should be absorbed by the other")
		}
	}
	if tc.reps[1][0].shard.searches.Load() == 0 {
		t.Fatal("overloaded replica never tried; test proves nothing")
	}
	if n := testutil.ToFloat64(tc.coord.metrics.ejections.WithLabelValues(tc.coord.parts[1][0].addr)); n != 0 {
		t.Errorf("overloaded replica ejected %v times", n)
	}
}

func TestWrongIndexReplicaIsNeverUsed(t *testing.T) {
	tc := newTestCluster(t, 1.0, Options{})
	tc.reps[1][1].shard.set(wrongCRC)
	r := tc.coord.parts[1][1]
	waitFor(t, "verification to fail", func() bool { return !r.isVerified() })
	tc.reps[1][0].kill()
	before := tc.reps[1][1].shard.searches.Load()
	resp := tc.identify(t, 3)
	if !resp.GetPartial() {
		t.Errorf("partition 1 has no verified replica; answer should be partial: %v", resp)
	}
	if tc.reps[1][1].shard.searches.Load() != before {
		t.Error("coordinator searched a replica serving the wrong index")
	}
}

func TestWrongPartitionAnswerIsRejected(t *testing.T) {
	tc := newTestCluster(t, 1.0, Options{HealthInterval: time.Hour}) // no re-verification during the test
	tc.reps[1][0].shard.set(wrongPartition)
	tc.reps[1][1].shard.set(wrongPartition)
	resp := tc.identify(t, 3)
	if !resp.GetPartial() || resp.GetMatched() {
		t.Fatalf("answers claiming the wrong partition must be discarded: %v", resp)
	}
	if tc.coord.parts[1][0].isVerified() || tc.coord.parts[1][1].isVerified() {
		t.Error("replicas answering for the wrong partition stay verified")
	}
}

func TestDrainingReplicaGetsNoTraffic(t *testing.T) {
	tc := newTestCluster(t, 1.0, Options{})
	draining := tc.reps[1][0]
	draining.health.SetServingStatus(landmarkv1.ShardService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_NOT_SERVING)
	waitFor(t, "health round", func() bool { return !tc.coord.parts[1][0].available() })
	before := draining.shard.searches.Load()
	for range 20 {
		tc.identify(t, 3)
	}
	if got := draining.shard.searches.Load() - before; got != 0 {
		t.Errorf("draining replica received %d searches", got)
	}
}

func TestIdentifyValidation(t *testing.T) {
	tc := newTestCluster(t, 1.0, Options{})
	resp, err := tc.coord.Identify(context.Background(), &landmarkv1.IdentifyRequest{})
	if err != nil || resp.GetPartial() || resp.GetPartitionsResponded() != 2 || len(resp.GetCandidates()) != 0 {
		t.Errorf("empty query: %v, %v", resp, err)
	}
	big := &landmarkv1.IdentifyRequest{Hashes: make([]uint32, shard.MaxLandmarks+1), Times: make([]uint32, shard.MaxLandmarks+1)}
	if _, err := tc.coord.Identify(context.Background(), big); status.Code(err) != codes.InvalidArgument {
		t.Errorf("too many landmarks: code %v", status.Code(err))
	}
	q := query(1)
	q.TopK = MaxTopK + 1
	if _, err := tc.coord.Identify(context.Background(), q); status.Code(err) != codes.InvalidArgument {
		t.Errorf("top_k too large: code %v", status.Code(err))
	}
	q = query(1)
	q.Times = q.Times[1:]
	if _, err := tc.coord.Identify(context.Background(), q); status.Code(err) != codes.InvalidArgument {
		t.Errorf("mismatched arrays: code %v", status.Code(err))
	}
}

func TestConcurrentQueriesSurviveReplicaKill(t *testing.T) {
	tc := newTestCluster(t, 1.0, Options{})
	var wg sync.WaitGroup
	var partial, failed atomic.Int64
	for i := range 200 {
		if i == 50 {
			tc.reps[0][1].kill()
			tc.reps[1][0].kill()
		}
		wg.Go(func() {
			resp, err := tc.coord.Identify(context.Background(), query(uint32(i%nTracks)))
			switch {
			case err != nil:
				failed.Add(1)
			case resp.GetPartial():
				partial.Add(1)
			}
		})
	}
	wg.Wait()
	if failed.Load() != 0 || partial.Load() != 0 {
		t.Errorf("with one live replica per partition: %d failed, %d partial", failed.Load(), partial.Load())
	}
}

// withStoplist lists hashes base+from .. base+to-1 with count 100 and
// base+to with count 7, and keeps hashes with at most 7 postings.
func withStoplist(t *testing.T, base, from, to uint32) func(*cluster.Cluster) {
	t.Helper()
	var hashes, counts []uint32
	for i := from; i < to; i++ {
		hashes, counts = append(hashes, base+i), append(counts, 100)
	}
	hashes, counts = append(hashes, base+to), append(counts, 7)
	l, err := stoplist.New(nTracks, 7, hashes, counts)
	if err != nil {
		t.Fatal(err)
	}
	return func(cl *cluster.Cluster) { cl.Stop, cl.StopLimit = l, 7 }
}

func (tc *testCluster) searches() int64 {
	var n int64
	for p := range tc.reps {
		for _, r := range tc.reps[p] {
			n += r.shard.searches.Load()
		}
	}
	return n
}

func TestStoplistDropsLandmarksBeforeFanOut(t *testing.T) {
	// Track 3's query has hashes 3005..3049. Drop 3005..3019 (count 100 > 7);
	// keep 3020 (count 7, at the limit). The shards see 30 landmarks.
	tc := newTestClusterWith(t, 1.0, Options{}, withStoplist(t, 3000, 5, 20))
	resp := tc.identify(t, 3)
	top := resp.GetCandidates()[0]
	if top.GetTrackId() != 3 || top.GetScore() != perTrack-5-15 || !resp.GetMatched() || resp.GetPartial() {
		t.Fatalf("top = %v, matched=%v partial=%v; want track 3 scoring %d", top, resp.GetMatched(), resp.GetPartial(), perTrack-5-15)
	}
	if got := testutil.ToFloat64(tc.coord.metrics.stopped); got != 15 {
		t.Errorf("landmarks_stopped_total = %v, want 15", got)
	}
}

func TestStoplistDroppingEveryLandmarkAnswersNoMatch(t *testing.T) {
	tc := newTestClusterWith(t, 1.0, Options{}, withStoplist(t, 1000, 5, perTrack))
	before := tc.searches()
	resp := tc.identify(t, 1)
	if len(resp.GetCandidates()) != 0 || resp.GetMatched() || resp.GetPartial() || resp.GetPartitionsResponded() != nPartitions {
		t.Fatalf("resp = %v, want a complete no-match", resp)
	}
	if n := tc.searches() - before; n != 0 {
		t.Errorf("%d shard searches for a query with no landmarks left, want 0", n)
	}
}

func TestStoplistLeavesUnlistedQueriesAlone(t *testing.T) {
	tc := newTestClusterWith(t, 1.0, Options{}, withStoplist(t, 3000, 5, 20))
	resp := tc.identify(t, 4) // no track-4 hash is listed
	if top := resp.GetCandidates()[0]; top.GetTrackId() != 4 || top.GetScore() != perTrack-5 {
		t.Fatalf("top = %v, want track 4 with the full score %d", top, perTrack-5)
	}
	if got := testutil.ToFloat64(tc.coord.metrics.stopped); got != 0 {
		t.Errorf("landmarks_stopped_total = %v, want 0", got)
	}
}

func TestStoplistFiltersTripletArraysInStep(t *testing.T) {
	tc := newTestClusterWith(t, 1.0, Options{}, withStoplist(t, 3000, 5, 20))
	q := query(3) // hashes 3005..3049; 3005..3019 are dropped
	for _, h := range q.Hashes {
		q.Spans = append(q.Spans, byte(h-3000))
		q.Bands = append(q.Bands, byte(h-2900))
	}
	// The test shards serve pair segments and reject triplet arrays; what
	// matters here is what reached them.
	if _, err := tc.coord.Identify(context.Background(), q); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code %v, want InvalidArgument from the pair shards", status.Code(err))
	}
	req := tc.reps[1][0].shard.last.Load()
	if req == nil {
		req = tc.reps[1][1].shard.last.Load()
	}
	if req == nil || len(req.GetHashes()) != 30 {
		t.Fatalf("shard request = %v, want the 30 landmarks left after the stop list", req)
	}
	for i, h := range req.GetHashes() {
		if req.GetSpans()[i] != byte(h-3000) || req.GetBands()[i] != byte(h-2900) {
			t.Fatalf("landmark %d: hash %d with span %d band %d: arrays out of step", i, h, req.GetSpans()[i], req.GetBands()[i])
		}
	}

	before := tc.searches()
	q.Bands = q.Bands[1:]
	if _, err := tc.coord.Identify(context.Background(), q); status.Code(err) != codes.InvalidArgument {
		t.Errorf("bands shorter than spans: code %v, want InvalidArgument", status.Code(err))
	}
	if n := tc.searches() - before; n != 0 {
		t.Errorf("%d shard searches for a malformed request, want 0", n)
	}
}
