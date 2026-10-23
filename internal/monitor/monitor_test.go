package monitor

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/audio"
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
	return setupURL(t, ttl, noiseWAV(t, seconds))
}

// noiseWAV writes a noise WAV of seconds and returns its path.
func noiseWAV(t *testing.T, seconds int) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	wav := filepath.Join(t.TempDir(), "noise.wav")
	if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i",
		"anoisesrc=seed=3:sample_rate=11025:duration="+itoa(seconds), wav).CombinedOutput(); err != nil {
		t.Fatalf("make fixture: %v: %s", err, out)
	}
	return wav
}

// setupURL runs a registry with one stream, s1 at url, over an in-memory
// connection.
func setupURL(t *testing.T, ttl time.Duration, url string) (*registry.Registry, *clock, landmarkv1.RegistryServiceClient) {
	t.Helper()
	c := &clock{t: time.Unix(1000, 0)}
	r, err := registry.Open(registry.Options{Dir: t.TempDir(), TTL: ttl, Now: c.now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	c.advance(ttl) // past the startup grace
	if _, err := r.AddStream(context.Background(), &landmarkv1.AddStreamRequest{
		Stream: &landmarkv1.Stream{Id: "s1", Url: url}}); err != nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute) // a wait, not a bound: fingerprinting slows on a busy machine
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
// the entries offline decoding gives for the same window answers, with
// each window fingerprinted on its own and with the shared front end
// (D48).
func TestWorkerLogsTheOfflineTracklist(t *testing.T) {
	for _, front := range []bool{false, true} {
		t.Run(map[bool]string{false: "per window", true: "shared front end"}[front], func(t *testing.T) {
			testWorkerLogsTheOfflineTracklist(t, front)
		})
	}
}

func testWorkerLogsTheOfflineTracklist(t *testing.T, front bool) {
	_, _, client := setup(t, 6*time.Second, 40) // 13 windows either way
	tracks := []int{5, 5, 5, 5, -1, -1, 9, 9, 9, 9, 9, 5, 5}
	s := &scripted{tracks: tracks}
	cfg := config(s, 50*time.Millisecond)
	hop := 27562
	if front {
		hop = 27520
		cfg.Front, cfg.HopSamples, cfg.Searcher.Shifts = true, hop, 4
	}
	w, err := New(cfg, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	var rows []tracklist.Row
	for i, tr := range tracks {
		r := tracklist.Row{T0: hybrid.WindowT0(i * hop), Mode: "b2", Factor: 1, Track: tr}
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
// first one's next append is refused and it stops the stream, for a file
// and for a live source (which must not take the refusal for a drop and
// join the source again).
func TestWorkerStopsWhenFenced(t *testing.T) {
	for _, live := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "live"}[live], func(t *testing.T) {
			// One heartbeat only (at start): a later one would also stop the
			// stream, before a slow machine gets to the refused append this
			// test is about.
			wav := noiseWAV(t, 40)
			url := wav
			var st *station
			if live {
				pcm, err := audio.Decode(context.Background(), wav, audio.SampleRate)
				if err != nil {
					t.Fatal(err)
				}
				st = &station{replies: []reply{{start: 1000, pcm: pcm}}}
				srv := httptest.NewServer(st)
				defer srv.Close()
				url = srv.URL + "/stream/s1"
			}
			r, c, client := setupURL(t, 10*time.Minute, url)
			release := make(chan struct{})
			s := &scripted{tracks: []int{5, 5, 5, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1}}
			s.gate = func(_ context.Context, n int) {
				if n == 3 {
					<-release // hold the fourth window until the takeover
				}
			}
			w, err := New(config(s, 5*time.Minute), client, nil)
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
			c.advance(11 * time.Minute) // w1's lease expires; w2 takes the stream
			resp, err := r.Heartbeat(context.Background(), &landmarkv1.HeartbeatRequest{WorkerId: "w2", Capacity: 1})
			if err != nil || len(resp.GetLeases()) != 1 || resp.GetLeases()[0].GetEpoch() != 2 {
				t.Fatalf("takeover: %v, %v", resp, err)
			}
			close(release)
			deadline := time.Now().Add(2 * time.Minute)
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
			if live && (testutil.ToFloat64(w.m.reconnects) != 0 || st.requests() != 1) {
				t.Errorf("joined the source again after being fenced (%d requests)", st.requests())
			}
			cancel()
			<-done
		})
	}
}

// failingRegistry answers heartbeats until one has succeeded, then fails
// every later one. (Counting calls instead failed on a slow CI runner: the
// first call timed out at its 100 ms deadline and used up the only answer.)
type failingRegistry struct {
	landmarkv1.RegistryServiceClient
	mu        sync.Mutex
	succeeded bool
}

func (f *failingRegistry) Heartbeat(ctx context.Context, req *landmarkv1.HeartbeatRequest, opts ...grpc.CallOption) (*landmarkv1.HeartbeatResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.succeeded {
		return nil, errors.New("registry unreachable")
	}
	resp, err := f.RegistryServiceClient.Heartbeat(ctx, req, opts...)
	f.succeeded = err == nil
	return resp, err
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

// station serves a live stream as cmd/streamserver does (float WAV with
// X-Landmark-Start), answering the n-th request with replies[n]: audio
// that then drops, a bare status, or (past the end) 410 Gone.
type station struct {
	mu      sync.Mutex
	replies []reply
	n       int
	times   []time.Time // when each request came
}

type reply struct {
	status int       // 0: 200 with pcm
	start  float64   // broadcast time of pcm's first sample
	pcm    []float32 // sent at once, then the connection closes
}

func (st *station) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	st.mu.Lock()
	r := reply{status: http.StatusGone}
	if st.n < len(st.replies) {
		r = st.replies[st.n]
	}
	st.n++
	st.times = append(st.times, time.Now())
	st.mu.Unlock()
	if r.status != 0 {
		http.Error(w, http.StatusText(r.status), r.status)
		return
	}
	w.Header().Set(StartHeader, strconv.FormatFloat(r.start, 'f', 6, 64))
	le := binary.LittleEndian
	b := append([]byte("RIFF"), le.AppendUint32(nil, 0xFFFFFFFF)...)
	b = append(b, "WAVEfmt "...)
	for _, v := range []uint32{16, 3 | 1<<16, audio.SampleRate, 4 * audio.SampleRate, 4 | 32<<16} {
		b = le.AppendUint32(b, v) // chunk size, float + mono, rate, byte rate, block align + bits
	}
	b = append(b, "data"...)
	b = le.AppendUint32(b, 0xFFFFFFFF)
	for _, v := range r.pcm {
		b = le.AppendUint32(b, math.Float32bits(v))
	}
	_, _ = w.Write(b)
}

func (st *station) requests() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.n
}

// TestLiveSourceRejoinsAfterADrop: a live stream that drops is joined
// again (retrying a failed try), and the song in progress continues as one
// entry that ends when the station goes off the air, not when the
// connection dropped; a song that had already ended is not continued.
func TestLiveSourceRejoinsAfterADrop(t *testing.T) {
	pcm, err := audio.Decode(context.Background(), noiseWAV(t, 60), audio.SampleRate)
	if err != nil {
		t.Fatal(err)
	}
	sec := func(a, b int) []float32 { return pcm[a*audio.SampleRate : b*audio.SampleRate] }
	// end of the last window of 20 s of audio (at 10 s, on the
	// 27,562-sample window grid)
	last := hybrid.WindowT0(4*27562) + 10
	type ev struct {
		kind       landmarkv1.EventKind
		start, end float64
		resumed    bool
	}
	start, end := landmarkv1.EventKind_EVENT_KIND_START, landmarkv1.EventKind_EVENT_KIND_END
	tests := []struct {
		name    string
		replies []reply
		tracks  []int // per window, both connections in order
		want    []ev  // start events: only the kind is checked
	}{
		{
			name: "song in progress continues",
			replies: []reply{{start: 1000, pcm: sec(0, 20)}, {status: http.StatusServiceUnavailable},
				{start: 1023, pcm: sec(20, 40)}}, // 3 s of broadcast missed
			tracks: slices.Repeat([]int{5}, 10),
			want:   []ev{{kind: start}, {kind: end, start: 1000, end: 1023 + last, resumed: true}},
		},
		{
			name:    "song that ended is not continued",
			replies: []reply{{start: 1000, pcm: sec(0, 40)}, {start: 1043, pcm: sec(40, 60)}},
			tracks:  slices.Concat([]int{5, 5}, slices.Repeat([]int{-1}, 11), slices.Repeat([]int{5}, 5)),
			want: []ev{{kind: start}, {kind: end, start: 1000, end: 1000 + hybrid.WindowT0(27562) + 10},
				{kind: start}, {kind: end, start: 1043, end: 1043 + last}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &station{replies: tt.replies}
			srv := httptest.NewServer(st)
			defer srv.Close()
			_, _, client := setupURL(t, 6*time.Second, srv.URL+"/stream/s1")
			w, err := New(config(&scripted{tracks: tt.tracks}, 50*time.Millisecond), client, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- w.Run(ctx) }()
			events := logged(t, client, len(tt.want))
			deadline := time.Now().Add(2 * time.Minute)
			for testutil.ToFloat64(w.m.finished) < 1 {
				if time.Now().After(deadline) {
					t.Fatal("stream never ended")
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			for i, e := range events {
				x := tt.want[i]
				if e.GetKind() != x.kind || e.GetTrackId() != 5 {
					t.Fatalf("event %d: %v of track %d, want %v of track 5", i, e.GetKind(), e.GetTrackId(), x.kind)
				}
				if x.kind == end && (e.GetResumed() != x.resumed || e.GetInterrupted() ||
					abs(e.GetStartUnix()-x.start) > 0.01 || abs(e.GetEndUnix()-x.end) > 0.01) {
					t.Errorf("event %d %+v; want %.3f to %.3f, resumed %v", i, e, x.start, x.end, x.resumed)
				}
			}
			if got := testutil.ToFloat64(w.m.reconnects); got != 1 {
				t.Errorf("joined again %v times, want 1", got)
			}
			if n := st.requests(); n != len(tt.replies)+1 { // then 410
				t.Errorf("%d requests, want %d", n, len(tt.replies)+1)
			}
		})
	}
}

// TestLiveSourceThatDropsAtOnceIsNotHammered: connections that end before
// a full window are joined again only after a backoff.
func TestLiveSourceThatDropsAtOnceIsNotHammered(t *testing.T) {
	pcm, err := audio.Decode(context.Background(), noiseWAV(t, 2), audio.SampleRate)
	if err != nil {
		t.Fatal(err)
	}
	st := &station{replies: []reply{{start: 1000, pcm: pcm}, {start: 1010, pcm: pcm}, {start: 1020, pcm: pcm}}}
	srv := httptest.NewServer(st)
	defer srv.Close()
	r, _, client := setupURL(t, 6*time.Second, srv.URL+"/stream/s1")
	w, err := New(config(&scripted{}, 50*time.Millisecond), client, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	deadline := time.Now().Add(time.Minute)
	for testutil.ToFloat64(w.m.finished) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("stream never ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	st.mu.Lock()
	times := slices.Clone(st.times)
	st.mu.Unlock()
	if len(times) != 4 { // three short connections, then 410
		t.Fatalf("%d requests, want 4", len(times))
	}
	for i, want := range []time.Duration{backoff(1), backoff(2), backoff(3)} {
		if gap := times[i+1].Sub(times[i]); gap < want {
			t.Errorf("request %d came %v after the one before, want at least %v", i+2, gap, want)
		}
	}
	list, err := r.ListStreams(context.Background(), &landmarkv1.ListStreamsRequest{})
	if err != nil || list.GetStreams()[0].GetNextSeq() != 1 {
		t.Errorf("log %v, %v; want it empty", list, err)
	}
}

func TestBackoff(t *testing.T) {
	for _, tt := range []struct {
		n    int
		want time.Duration
	}{{1, 500 * time.Millisecond}, {2, time.Second}, {3, 2 * time.Second}, {5, 8 * time.Second},
		{6, 10 * time.Second}, {100, 10 * time.Second}} {
		if got := backoff(tt.n); got != tt.want {
			t.Errorf("backoff(%d) = %v, want %v", tt.n, got, tt.want)
		}
	}
}

// TestLiveSourceOffTheAir: a stream whose station is already gone ends
// at once, with nothing to log.
func TestLiveSourceOffTheAir(t *testing.T) {
	st := &station{}
	srv := httptest.NewServer(st)
	defer srv.Close()
	r, _, client := setupURL(t, 6*time.Second, srv.URL+"/stream/s1")
	w, err := New(config(&scripted{}, 50*time.Millisecond), client, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	deadline := time.Now().Add(time.Minute)
	for testutil.ToFloat64(w.m.finished) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("stream never ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	list, err := r.ListStreams(context.Background(), &landmarkv1.ListStreamsRequest{})
	if err != nil || list.GetStreams()[0].GetNextSeq() != 1 {
		t.Errorf("log %v, %v; want it empty", list, err)
	}
	if n := st.requests(); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}
