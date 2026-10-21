package monitor

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/hybrid"
	"github.com/anytroops/landmark/internal/registry"
	"github.com/anytroops/landmark/internal/tracklist"
)

func TestWindower(t *testing.T) {
	c := windower{size: 4, hop: 2}
	var starts []int
	var firsts []float32
	for _, chunk := range [][]float32{{0, 1, 2}, {3}, {4, 5, 6, 7, 8}, {}, {9}} {
		for _, w := range c.add(chunk) {
			if len(w.x) != 4 {
				t.Fatalf("window of %d samples", len(w.x))
			}
			starts, firsts = append(starts, w.start), append(firsts, w.x[0])
		}
	}
	// Windows at 0, 2, 4, 6 (samples 0-9 hold four full windows).
	if want := []int{0, 2, 4, 6}; !equal(starts, want) {
		t.Fatalf("starts %v, want %v", starts, want)
	}
	for i, f := range firsts {
		if int(f) != starts[i] {
			t.Errorf("window %d starts with sample %v", i, f)
		}
	}
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// clock is a settable test clock for the registry.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// setup runs a registry with one stream, a noise WAV of seconds, over an
// in-memory connection.
func setup(t *testing.T, ttl time.Duration, seconds int) (*registry.Registry, *clock, landmarkv1.RegistryServiceClient) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	wav := filepath.Join(t.TempDir(), "noise.wav")
	if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i",
		"anoisesrc=seed=3:sample_rate=11025:duration="+itoa(seconds), wav).CombinedOutput(); err != nil {
		t.Fatalf("make fixture: %v: %s", err, out)
	}
	c := &clock{t: time.Unix(1000, 0)}
	r, err := registry.Open(registry.Options{Dir: t.TempDir(), TTL: ttl, Now: c.now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	c.advance(ttl) // past the startup grace
	if _, err := r.AddStream(context.Background(), &landmarkv1.AddStreamRequest{
		Stream: &landmarkv1.Stream{Id: "s1", Url: wav}}); err != nil {
		t.Fatal(err)
	}
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	landmarkv1.RegisterRegistryServiceServer(gs, r)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return r, c, landmarkv1.NewRegistryServiceClient(conn)
}

func itoa(i int) string {
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

// scripted answers the n-th triplet search with tracks[n] (-1: nothing),
// after gate (if set) lets call n through.
type scripted struct {
	mu     sync.Mutex
	tracks []int
	n      int
	gate   func(ctx context.Context, n int)
}

func (s *scripted) identify(ctx context.Context, _ *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
	s.mu.Lock()
	n := s.n
	s.n++
	s.mu.Unlock()
	if s.gate != nil {
		s.gate(ctx, n)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n >= len(s.tracks) || s.tracks[n] < 0 {
		return &landmarkv1.IdentifyResponse{}, nil
	}
	return &landmarkv1.IdentifyResponse{Candidates: []*landmarkv1.Candidate{{TrackId: uint32(s.tracks[n]), Score: 50}}}, nil
}

func config(s *scripted, heartbeat time.Duration) Config {
	return Config{WorkerID: "w1", Capacity: 1, Heartbeat: heartbeat, Window: 10, Hop: 2.5,
		TAbs: 10, TRatio: 2, K: 1, MergeGap: 2.5, ResumeWithin: 10, ReadRate: 0, SearchRetries: 1,
		Searcher: &hybrid.Searcher{B2: s.identify, Cluster: func(d uint32) uint32 { return d }, Shifts: 1, Timeout: time.Second}}
}

// logged waits until the stream's log holds n events and returns them.
func logged(t *testing.T, client landmarkv1.RegistryServiceClient, n int) []*landmarkv1.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sub, err := client.Subscribe(ctx, &landmarkv1.SubscribeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var out []*landmarkv1.Event
	for len(out) < n {
		m, err := sub.Recv()
		if err != nil {
			t.Fatalf("after %d of %d events: %v", len(out), n, err)
		}
		out = append(out, m.GetEvent())
	}
	return out
}

// TestWorkerLogsTheOfflineTracklist: a file source decoded to its end logs
// the entries offline decoding gives for the same window answers.
func TestWorkerLogsTheOfflineTracklist(t *testing.T) {
	_, _, client := setup(t, 6*time.Second, 40) // 13 windows
	tracks := []int{5, 5, 5, 5, -1, -1, 9, 9, 9, 9, 9, 5, 5}
	s := &scripted{tracks: tracks}
	w, err := New(config(s, 50*time.Millisecond), client, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	var rows []tracklist.Row
	for i, tr := range tracks {
		r := tracklist.Row{T0: hybrid.WindowT0(i * 27562), Mode: "b2", Factor: 1, Track: tr}
		if tr >= 0 {
			r.Score = 50
		}
		rows = append(rows, r)
	}
	want := tracklist.MergeRepeats(tracklist.Smooth(rows, 10, 10, 2, 1), 2.5)
	events := logged(t, client, 2*len(want))
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var ends []*landmarkv1.Event
	for i, e := range events {
		if e.GetSeq() != uint64(i+1) || e.GetEpoch() != 1 || e.GetWorkerId() != "w1" {
			t.Errorf("event %d: seq %d epoch %d worker %q", i, e.GetSeq(), e.GetEpoch(), e.GetWorkerId())
		}
		if e.GetKind() == landmarkv1.EventKind_EVENT_KIND_END {
			ends = append(ends, e)
		}
	}
	if len(ends) != len(want) {
		t.Fatalf("%d entries logged, offline %d", len(ends), len(want))
	}
	base := ends[0].GetStartUnix() - want[0].Start
	for i, e := range ends {
		g := tracklist.Entry{Track: int(e.GetTrackId()), Start: e.GetStartUnix() - base, End: e.GetEndUnix() - base,
			Factor: e.GetTempo(), Evidence: e.GetEvidence()}
		x := want[i]
		if g.Track != x.Track || g.Evidence != x.Evidence || g.Factor != x.Factor ||
			abs(g.Start-x.Start) > 1e-6 || abs(g.End-x.End) > 1e-6 {
			t.Errorf("entry %d logged %+v, offline %+v", i, g, x)
		}
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// TestWorkerStopsWhenFenced: once another worker holds the stream, the
// first one's next append is refused and it stops the stream.
func TestWorkerStopsWhenFenced(t *testing.T) {
	r, c, client := setup(t, 30*time.Second, 40)
	release := make(chan struct{})
	s := &scripted{tracks: []int{5, 5, 5, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1}}
	s.gate = func(_ context.Context, n int) {
		if n == 3 {
			<-release // hold the fourth window until the takeover
		}
	}
	w, err := New(config(s, 10*time.Second), client, nil) // no renewal during the test
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	if e := logged(t, client, 1)[0]; e.GetKind() != landmarkv1.EventKind_EVENT_KIND_START || e.GetEpoch() != 1 {
		t.Fatalf("first event %+v", e)
	}
	c.advance(time.Minute) // w1's lease expires; w2 takes the stream
	resp, err := r.Heartbeat(context.Background(), &landmarkv1.HeartbeatRequest{WorkerId: "w2", Capacity: 1})
	if err != nil || len(resp.GetLeases()) != 1 || resp.GetLeases()[0].GetEpoch() != 2 {
		t.Fatalf("takeover: %v, %v", resp, err)
	}
	close(release)
	deadline := time.Now().Add(20 * time.Second)
	for testutil.ToFloat64(w.m.fenced) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("worker never fenced")
		}
		time.Sleep(10 * time.Millisecond)
	}
	list, err := r.ListStreams(context.Background(), &landmarkv1.ListStreamsRequest{})
	if err != nil || list.GetStreams()[0].GetNextSeq() != 2 {
		t.Errorf("log after fencing: %v, %v; want only the first event", list, err)
	}
	cancel()
	<-done
}

// failingRegistry answers the first heartbeat and fails every later one.
type failingRegistry struct {
	landmarkv1.RegistryServiceClient
	mu    sync.Mutex
	calls int
}

func (f *failingRegistry) Heartbeat(ctx context.Context, req *landmarkv1.HeartbeatRequest, opts ...grpc.CallOption) (*landmarkv1.HeartbeatResponse, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()
	if n > 1 {
		return nil, errors.New("registry unreachable")
	}
	return f.RegistryServiceClient.Heartbeat(ctx, req, opts...)
}

// TestWorkerStopsWithoutHeartbeats: a worker cut off from the registry
// stops its streams before its lease can expire there.
func TestWorkerStopsWithoutHeartbeats(t *testing.T) {
	_, _, client := setup(t, 400*time.Millisecond, 40)
	s := &scripted{tracks: make([]int, 100)}
	cancelled := make(chan time.Time, 1)
	s.gate = func(ctx context.Context, _ int) { // a search that waits until cancelled
		<-ctx.Done()
		select {
		case cancelled <- time.Now():
		default:
		}
	}
	cfg := config(s, 100*time.Millisecond)
	cfg.Window, cfg.Hop = 1, 0.5 // reach the blocking search well before the deadline
	w, err := New(cfg, &failingRegistry{RegistryServiceClient: client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for testutil.ToFloat64(w.m.running) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("stream never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	var stopped time.Time
	select {
	case stopped = <-cancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("stream still running without heartbeats")
	}
	// Due 300 ms after the last heartbeat (TTL 400 minus one 100 ms interval).
	if d := stopped.Sub(start); d > 350*time.Millisecond {
		t.Errorf("stopped %v after the last heartbeat, want about 300 ms (lease 400 ms)", d)
	}
	cancel()
	<-done
}

// TestEndedSourcesFreeCapacity: with capacity 1, a second file stream runs
// once the first has ended.
func TestEndedSourcesFreeCapacity(t *testing.T) {
	r, _, client := setup(t, 6*time.Second, 15)
	list, err := r.ListStreams(context.Background(), &landmarkv1.ListStreamsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddStream(context.Background(), &landmarkv1.AddStreamRequest{
		Stream: &landmarkv1.Stream{Id: "s2", Url: list.GetStreams()[0].GetStream().GetUrl()}}); err != nil {
		t.Fatal(err)
	}
	s := &scripted{tracks: []int{4, 4, 4, 4, 4, 4, 4, 4, 4, 4}}
	w, err := New(config(s, 50*time.Millisecond), client, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	streams := map[string]int{}
	for _, e := range logged(t, client, 4) { // a start and an end per stream
		streams[e.GetStreamId()]++
		if e.GetSourceStartUnix() == 0 {
			t.Errorf("event without its source start: %+v", e)
		}
	}
	cancel()
	<-done
	if streams["s1"] != 2 || streams["s2"] != 2 {
		t.Errorf("events per stream %v, want 2 each", streams)
	}
}
