package coordinator

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/cluster"
	"github.com/anytroops/landmark/internal/manifest"
	"github.com/anytroops/landmark/internal/segment"
	"github.com/anytroops/landmark/internal/shard"
)

// added is the song version 2 adds: global id 7, partition 1, local id 3.
const added = 7

// versionedIndex publishes version 1 (tracks 0-5, as newTestCluster) and
// version 2 (track 7 added to partition 1 as a delta) in a directory.
func versionedIndex(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, p, ntracks uint32, tracks []uint32) manifest.Member {
		var entries []segment.Entry
		for _, d := range tracks {
			for i := range uint32(perTrack) {
				entries = append(entries, segment.Entry{Hash: trackHashBase(d) + i, Track: d / nPartitions, T: i})
			}
		}
		meta := segment.Meta{Partition: p, NPartitions: nPartitions, NTracks: ntracks, Encoding: segment.EncodingVarint}
		if err := segment.Write(filepath.Join(dir, name), meta, entries); err != nil {
			t.Fatal(err)
		}
		s, err := segment.Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		return manifest.Member{File: name, CRC32C: manifest.FormatCRC(s.Checksum()), NTracks: ntracks}
	}
	b0 := write("p0.lmrk", 0, 3, []uint32{0, 2, 4})
	b1 := write("p1.lmrk", 1, 3, []uint32{1, 3, 5})
	d1 := write("p1.d2.lmrk", 1, 4, []uint32{added})
	for _, m := range []manifest.Manifest{
		{Version: 1, NPartitions: 2, NTracks: 6, Partitions: [][]manifest.Member{{b0}, {b1}}, Catalog: "catalog.json"},
		{Version: 2, NPartitions: 2, NTracks: 8, Partitions: [][]manifest.Member{{b0}, {b1, d1}}, Catalog: "catalog-2.json"},
	} {
		if err := manifest.Write(dir, m); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// newVersionedCluster runs replica k of partition p at version at[p][k].
func newVersionedCluster(t *testing.T, at [nPartitions][2]uint64, opts Options) *testCluster {
	t.Helper()
	dir := versionedIndex(t)
	tc := &testCluster{}
	for p := range uint32(nPartitions) {
		for k := range 2 {
			m, err := manifest.Read(dir, at[p][k])
			if err != nil {
				t.Fatal(err)
			}
			set, err := manifest.Open(dir, m, p)
			if err != nil {
				t.Fatal(err)
			}
			sum, _ := m.Checksum(p)
			srv, err := shard.New(shard.Content{Set: set, Version: m.Version, Checksum: sum},
				shard.Config{Workers: 4, MaxQueueWait: time.Second}, prometheus.NewRegistry())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(srv.Close)
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
	cl := &cluster.Cluster{
		Config: cluster.Config{
			IndexDir: dir, DeadlineMs: int(deadline.Milliseconds()), TAbs: 10, TRatio: 1,
			Partitions: []cluster.Partition{
				{ID: 0, Replicas: []string{tc.reps[0][0].addr, tc.reps[0][1].addr}},
				{ID: 1, Replicas: []string{tc.reps[1][0].addr, tc.reps[1][1].addr}},
			},
		},
		ExpectedCRC: make([]uint32, nPartitions),
		Catalog:     catalog,
	}
	if opts.HealthInterval == 0 {
		opts.HealthInterval = 50 * time.Millisecond
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

func TestQueriesUseOneVersion(t *testing.T) {
	// Partition 1's second replica lags at version 1.
	tc := newVersionedCluster(t, [nPartitions][2]uint64{{2, 2}, {2, 1}}, Options{})
	lagging := tc.reps[1][1].shard
	for range 10 {
		resp := tc.identify(t, added)
		if resp.GetIndexVersion() != 2 || resp.GetPartial() || resp.GetCandidates()[0].GetTrackId() != added {
			t.Fatalf("query for the added song: %v", resp)
		}
	}
	if n := lagging.searches.Load(); n != 0 {
		t.Errorf("the replica still at version 1 got %d searches pinned to version 2", n)
	}
	if v := tc.reps[0][0].shard.last.Load().GetManifestVersion(); v != 2 {
		t.Errorf("searches pinned to version %d, want 2", v)
	}
}

func TestPartitionWithoutTheVersionIsLeftOut(t *testing.T) {
	// Partition 0's replicas hold only version 2, partition 1's only version 1:
	// no version is complete. Mixing them is never an option.
	tc := newVersionedCluster(t, [nPartitions][2]uint64{{2, 2}, {1, 1}}, Options{})
	resp := tc.identify(t, 2)
	if !resp.GetPartial() || resp.GetIndexVersion() != 2 || resp.GetPartitionsResponded() != 1 {
		t.Fatalf("answer %v: want a partial answer from version 2", resp)
	}
	if n := tc.reps[1][0].shard.searches.Load() + tc.reps[1][1].shard.searches.Load(); n != 0 {
		t.Errorf("partition 1 (version 1 only) got %d searches", n)
	}
}

func TestReplicaPastThePinnedVersionIsNotEjected(t *testing.T) {
	tc := newVersionedCluster(t, [nPartitions][2]uint64{{2, 2}, {2, 2}}, Options{EjectAfter: 2})
	moved := tc.reps[1][0]
	moved.shard.set(movedOn) // swapped on before the health loop noticed
	for i := range 20 {
		if resp := tc.identify(t, added); resp.GetPartial() || resp.GetCandidates()[0].GetTrackId() != added {
			t.Fatalf("query %d: %v; the other replica should answer", i, resp)
		}
	}
	if moved.shard.searches.Load() == 0 {
		t.Fatal("the moved-on replica was never tried; the test proves nothing")
	}
	addr := tc.coord.parts[1][0].addr
	if n := testutil.ToFloat64(tc.coord.metrics.ejections.WithLabelValues(addr)); n != 0 {
		t.Errorf("replica that moved past the version ejected %v times", n)
	}
}
