package main

import (
	"context"
	"net"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/triplet"
)

func TestComputeStats(t *testing.T) {
	var recs []record
	for i := 1; i <= 100; i++ { // latencies 1..100 ms, all OK
		recs = append(recs, record{Latency: time.Duration(i) * time.Millisecond, Code: "OK", Correct: true})
	}
	recs = append(recs,
		record{Code: "DROPPED"},
		record{Latency: 500 * time.Millisecond, Code: "DeadlineExceeded"},
		record{Latency: 3 * time.Millisecond, Code: "OK", Partial: true},
	)
	s := computeStats(recs)
	if s.Count != 103 || s.OK != 101 || s.Partial != 1 || s.Dropped != 1 {
		t.Fatalf("counts: %+v", s)
	}
	if want := 100.0 / 103; s.SuccessRate != want {
		t.Errorf("success rate %v, want %v (partial and failures excluded)", s.SuccessRate, want)
	}
	// Percentiles include the failed (500 ms) and partial (3 ms) requests but
	// not the dropped one: 102 latencies, so the nearest-rank median is the
	// 51st, which is 50 ms.
	if s.P50Ms != 50 || s.MaxMs != 500 {
		t.Errorf("p50 %v max %v", s.P50Ms, s.MaxMs)
	}
}

// slowCoordinator answers every Identify after a fixed delay.
type slowCoordinator struct {
	landmarkv1.UnimplementedCoordinatorServiceServer
	delay time.Duration
}

func (s slowCoordinator) Identify(ctx context.Context, _ *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
	}
	return &landmarkv1.IdentifyResponse{PartitionsResponded: 1, PartitionsTotal: 1}, nil
}

// TestOpenLoop checks the defining property: a slow server does not slow
// the sending schedule. 100 qps for 1 s against a 50 ms server must still
// send all 100 requests on time, about 5 in flight at once.
func TestOpenLoop(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	landmarkv1.RegisterCoordinatorServiceServer(gs, slowCoordinator{delay: 50 * time.Millisecond})
	go func() { _ = gs.Serve(lis) }()
	defer gs.Stop()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	cfg := runConfig{Rate: 100, Duration: time.Second, Timeout: time.Second, MaxInFlight: 1000}
	queries := []Query{{Expected: -1, Landmarks: []fingerprint.Landmark{{Hash: 1}}}}
	reqs := []*landmarkv1.IdentifyRequest{{Hashes: []uint32{1}, Times: []uint32{0}}}
	start := time.Now()
	recs, _ := schedule(cfg, queries, reqs, []landmarkv1.CoordinatorServiceClient{landmarkv1.NewCoordinatorServiceClient(conn)})
	if len(recs) != 100 {
		t.Fatalf("scheduled %d requests, want 100", len(recs))
	}
	if el := time.Since(start); el > 1500*time.Millisecond {
		t.Errorf("run took %v; a closed loop would need ~5 s", el)
	}
	for i, r := range recs {
		if r.Code != "OK" || r.Latency < 50*time.Millisecond {
			t.Fatalf("request %d: %+v", i, r)
		}
		if want := time.Duration(i) * 10 * time.Millisecond; r.Due != want {
			t.Fatalf("request %d due at %v, want %v", i, r.Due, want)
		}
	}
}

func TestIdentifyRequest(t *testing.T) {
	pairs := identifyRequest(Query{Landmarks: []fingerprint.Landmark{{Hash: 7, T: 3}}})
	if !slices.Equal(pairs.GetHashes(), []uint32{7}) || !slices.Equal(pairs.GetTimes(), []uint32{3}) ||
		len(pairs.GetSpans()) != 0 || len(pairs.GetBands()) != 0 {
		t.Errorf("pair request = %v", pairs)
	}
	trips := identifyRequest(Query{Triplets: []triplet.Landmark{{Hash: 9, T: 4, Span: 20, Band: 60}, {Hash: 11, T: 5, Span: 30, Band: 70}}})
	if !slices.Equal(trips.GetHashes(), []uint32{9, 11}) || !slices.Equal(trips.GetTimes(), []uint32{4, 5}) ||
		!slices.Equal(trips.GetSpans(), []byte{20, 30}) || !slices.Equal(trips.GetBands(), []byte{60, 70}) {
		t.Errorf("triplet request = %v", trips)
	}
}
