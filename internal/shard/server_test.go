package shard

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/segment"
)

// testSegment writes partition 1 of 2: local track 0 is global 1, local 3 is global 7.
// Local track 3 holds hashes 10..29 at t = hash; local track 0 holds hashes 10..14.
func testSegment(t *testing.T) *segment.Segment {
	t.Helper()
	var entries []segment.Entry
	for h := uint32(10); h < 30; h++ {
		entries = append(entries, segment.Entry{Hash: h, Track: 3, T: h})
	}
	for h := uint32(10); h < 15; h++ {
		entries = append(entries, segment.Entry{Hash: h, Track: 0, T: h + 100})
	}
	path := filepath.Join(t.TempDir(), "p1.lmrk")
	if err := segment.Write(path, segment.Meta{Partition: 1, NPartitions: 2, NTracks: 4,
		Encoding: segment.EncodingVarint}, entries); err != nil {
		t.Fatal(err)
	}
	seg, err := segment.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = seg.Close() })
	return seg
}

// startServer runs srv over an in-memory listener and returns a client.
func startServer(t *testing.T, srv *Server) landmarkv1.ShardServiceClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	landmarkv1.RegisterShardServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return landmarkv1.NewShardServiceClient(conn)
}

func newServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	srv, err := New(testSegment(t), cfg, prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// query asks for hashes 10..29 at t = hash - 5, so global track 7 aligns at offset 5.
func query() *landmarkv1.SearchRequest {
	req := &landmarkv1.SearchRequest{TopK: 5}
	for h := uint32(10); h < 30; h++ {
		req.Hashes = append(req.Hashes, h)
		req.Times = append(req.Times, h-5)
	}
	return req
}

func TestSearchReturnsGlobalIDs(t *testing.T) {
	client := startServer(t, newServer(t, Config{Workers: 2, MaxQueueWait: time.Second}))
	resp, err := client.Search(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	want := []*landmarkv1.Candidate{
		{TrackId: 7, Score: 20, Offset: 5},
		{TrackId: 1, Score: 5, Offset: 105},
	}
	if resp.GetPartitionId() != 1 || resp.GetPostingsScanned() != 25 || len(resp.GetCandidates()) != len(want) {
		t.Fatalf("got %v", resp)
	}
	for i := range want {
		if !proto.Equal(resp.GetCandidates()[i], want[i]) {
			t.Errorf("candidate %d = %v, want %v", i, resp.GetCandidates()[i], want[i])
		}
	}
}

func TestSearchValidation(t *testing.T) {
	client := startServer(t, newServer(t, Config{Workers: 1, MaxQueueWait: time.Second}))
	big := &landmarkv1.SearchRequest{Hashes: make([]uint32, MaxLandmarks+1), Times: make([]uint32, MaxLandmarks+1)}
	tests := []struct {
		name string
		req  *landmarkv1.SearchRequest
	}{
		{"no landmarks", &landmarkv1.SearchRequest{}},
		{"too many landmarks", big},
		{"top_k too large", &landmarkv1.SearchRequest{Hashes: query().Hashes, Times: query().Times, TopK: MaxTopK + 1}},
		{"mismatched arrays", &landmarkv1.SearchRequest{Hashes: []uint32{1, 2}, Times: []uint32{1}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.Search(context.Background(), tc.req)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
			}
		})
	}
}

func TestOverloadBehavior(t *testing.T) {
	srv := newServer(t, Config{Workers: 1, MaxQueueWait: 50 * time.Millisecond})
	client := startServer(t, srv)
	held := <-srv.pool // occupy the only worker

	t.Run("queue wait exceeded is RESOURCE_EXHAUSTED", func(t *testing.T) {
		start := time.Now()
		_, err := client.Search(context.Background(), query())
		if status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("code = %v, want ResourceExhausted", status.Code(err))
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("rejection took %v; should be about MaxQueueWait", d)
		}
	})
	t.Run("caller deadline first is DEADLINE_EXCEEDED", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		_, err := client.Search(ctx, query())
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("code = %v, want DeadlineExceeded", status.Code(err))
		}
	})

	srv.pool <- held
	if _, err := client.Search(context.Background(), query()); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestConcurrentSearchesAgree(t *testing.T) {
	client := startServer(t, newServer(t, Config{Workers: 4, MaxQueueWait: 5 * time.Second}))
	want, err := client.Search(context.Background(), query())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := client.Search(context.Background(), query())
			if err != nil {
				errs <- err
				return
			}
			if !proto.Equal(got, want) {
				errs <- status.Error(codes.Internal, "concurrent result differs")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestInfoReportsSegmentIdentity(t *testing.T) {
	srv := newServer(t, Config{Workers: 1, MaxQueueWait: time.Second})
	client := startServer(t, srv)
	held := <-srv.pool // Info must answer even with every worker busy
	defer func() { srv.pool <- held }()
	info, err := client.Info(context.Background(), &landmarkv1.InfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if info.GetPartitionId() != 1 || info.GetNPartitions() != 2 || info.GetNTracks() != 4 ||
		info.GetNPostings() != 25 || info.GetSegmentCrc32C() != srv.seg.Checksum() || info.GetFormatVersion() != 1 {
		t.Fatalf("Info = %v", info)
	}
}
