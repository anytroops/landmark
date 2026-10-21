package registry

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
)

const ttl = 6 * time.Second

// clock is a settable test clock.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

// open opens a registry in dir with the clock past the startup grace.
func open(t *testing.T, dir string, c *clock) *Registry {
	t.Helper()
	r, err := Open(Options{Dir: dir, TTL: ttl, Now: c.now, SubscriberBuffer: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	c.advance(ttl)
	return r
}

func addStreams(t *testing.T, r *Registry, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := r.AddStream(context.Background(), &landmarkv1.AddStreamRequest{
			Stream: &landmarkv1.Stream{Id: id, Url: "http://example.test/" + id}}); err != nil {
			t.Fatal(err)
		}
	}
}

func heartbeat(t *testing.T, r *Registry, worker string, capacity uint32, held ...*landmarkv1.Lease) []*landmarkv1.Lease {
	t.Helper()
	req := &landmarkv1.HeartbeatRequest{WorkerId: worker, Capacity: capacity}
	for _, l := range held {
		req.Held = append(req.Held, &landmarkv1.HeldLease{StreamId: l.GetStreamId(), Epoch: l.GetEpoch()})
	}
	resp, err := r.Heartbeat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetLeases()
}

func ids(leases []*landmarkv1.Lease) []string {
	var out []string
	for _, l := range leases {
		out = append(out, l.GetStreamId())
	}
	return out
}

func event(seq uint64, kind landmarkv1.EventKind, track uint32) *landmarkv1.Event {
	return &landmarkv1.Event{Seq: seq, Kind: kind, TrackId: track, StartUnix: float64(seq), WorkerId: "w"}
}

func appendEvents(r *Registry, stream string, epoch uint64, events ...*landmarkv1.Event) (uint64, error) {
	resp, err := r.Append(context.Background(), &landmarkv1.AppendRequest{StreamId: stream, Epoch: epoch, Events: events})
	return resp.GetNextSeq(), err
}

func TestLeasesGrantRenewExpire(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	r := open(t, t.TempDir(), c)
	addStreams(t, r, "c", "a", "b")

	a := heartbeat(t, r, "w1", 2)
	if got := ids(a); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("w1 leases %v, want a b", got)
	}
	if a[0].GetEpoch() != 1 || a[0].GetNextSeq() != 1 {
		t.Errorf("first grant %+v, want epoch 1, next seq 1", a[0])
	}
	if got := ids(heartbeat(t, r, "w2", 5)); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("w2 leases %v, want c", got)
	}

	// w1 renews within the TTL and keeps its leases at the same epoch.
	c.advance(4 * time.Second)
	a = heartbeat(t, r, "w1", 2, a...)
	c.advance(4 * time.Second)
	a = heartbeat(t, r, "w1", 2, a...)
	if got := ids(a); !slices.Equal(got, []string{"a", "b"}) || a[0].GetEpoch() != 1 {
		t.Fatalf("after renewals %v epoch %d", got, a[0].GetEpoch())
	}

	// w1 stops renewing; past the TTL w2 takes its streams at a new epoch
	// (and c again: w2 did not renew it either).
	c.advance(ttl + time.Millisecond)
	w2 := heartbeat(t, r, "w2", 5)
	if got := ids(w2); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("w2 leases after w1 died %v, want a b c", got)
	}
	if w2[0].GetEpoch() != 2 {
		t.Errorf("regrant at epoch %d, want 2", w2[0].GetEpoch())
	}
	// w1 comes back holding epoch 1: renewal refused, no leases.
	if got := heartbeat(t, r, "w1", 2, a...); len(got) != 0 {
		t.Errorf("stale holder got %v back", ids(got))
	}
}

func TestExpiredLeaseCanBeTakenBackIfNotRegranted(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	r := open(t, t.TempDir(), c)
	addStreams(t, r, "a")
	l := heartbeat(t, r, "w1", 1)
	c.advance(ttl + time.Second) // late, but no one else asked
	if got := heartbeat(t, r, "w1", 0, l...); len(got) != 1 || got[0].GetEpoch() != 1 {
		t.Fatalf("late renewal: %v", got)
	}
}

func TestGrantNotReceivedIsSentAgain(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	r := open(t, t.TempDir(), c)
	addStreams(t, r, "a")
	heartbeat(t, r, "w1", 1) // reply lost: the worker does not know it holds a
	c.advance(2 * time.Second)
	if got := heartbeat(t, r, "w1", 1); len(got) != 1 || got[0].GetEpoch() != 1 {
		t.Fatalf("second heartbeat: %v, want the same lease again", got)
	}
}

func TestAppendFencesStaleEpochs(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	r := open(t, t.TempDir(), c)
	addStreams(t, r, "a")
	old := heartbeat(t, r, "w1", 1)[0]
	if _, err := appendEvents(r, "a", old.GetEpoch(), event(1, landmarkv1.EventKind_EVENT_KIND_START, 7)); err != nil {
		t.Fatal(err)
	}
	c.advance(ttl + time.Second)
	cur := heartbeat(t, r, "w2", 1)[0]
	if cur.GetEpoch() != 2 || cur.GetNextSeq() != 2 || cur.GetOpen().GetTrackId() != 7 {
		t.Fatalf("new lease %+v, want epoch 2, next seq 2, open track 7", cur)
	}
	_, err := appendEvents(r, "a", old.GetEpoch(), event(2, landmarkv1.EventKind_EVENT_KIND_END, 7))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("append at the old epoch: %v, want FailedPrecondition", err)
	}
	if next, err := appendEvents(r, "a", cur.GetEpoch(), event(2, landmarkv1.EventKind_EVENT_KIND_END, 7)); err != nil || next != 3 {
		t.Fatalf("append at the current epoch: next %d, %v", next, err)
	}
}

func TestAppendSequence(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	r := open(t, t.TempDir(), c)
	addStreams(t, r, "a")
	ep := heartbeat(t, r, "w1", 1)[0].GetEpoch()
	start, end := event(1, landmarkv1.EventKind_EVENT_KIND_START, 7), event(2, landmarkv1.EventKind_EVENT_KIND_END, 7)
	if _, err := appendEvents(r, "a", ep, start); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		events []*landmarkv1.Event
		code   codes.Code
		next   uint64
	}{
		{"exact re-send is acknowledged", []*landmarkv1.Event{start}, codes.OK, 2},
		{"changed re-send is refused", []*landmarkv1.Event{event(1, landmarkv1.EventKind_EVENT_KIND_START, 8)}, codes.FailedPrecondition, 0},
		{"gap is refused", []*landmarkv1.Event{event(3, landmarkv1.EventKind_EVENT_KIND_START, 8)}, codes.FailedPrecondition, 0},
		{"seq 0 is refused", []*landmarkv1.Event{event(0, landmarkv1.EventKind_EVENT_KIND_START, 8)}, codes.InvalidArgument, 0},
		{"non-consecutive batch is refused", []*landmarkv1.Event{end, end}, codes.InvalidArgument, 0},
		{"re-send plus new events", []*landmarkv1.Event{start, end}, codes.OK, 3},
		{"unknown stream", nil, codes.NotFound, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := "a"
			if tt.code == codes.NotFound {
				stream = "zzz"
			}
			next, err := appendEvents(r, stream, ep, tt.events...)
			if status.Code(err) != tt.code || (err == nil && next != tt.next) {
				t.Fatalf("next %d, err %v; want %v, next %d", next, err, tt.code, tt.next)
			}
		})
	}
}

func TestRestartKeepsEpochsAndLogsAndWaitsOneTTL(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Unix(1000, 0)}
	r := open(t, dir, c)
	addStreams(t, r, "a")
	l := heartbeat(t, r, "w1", 1)[0]
	if _, err := appendEvents(r, "a", l.GetEpoch(), event(1, landmarkv1.EventKind_EVENT_KIND_START, 7)); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	// A crash mid-append left half a line.
	f, err := os.OpenFile(filepath.Join(dir, "events", "a.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":"2","kind":"EVENT_K`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	r2, err := Open(Options{Dir: dir, TTL: ttl, Now: c.now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r2.Close() }()
	if got := heartbeat(t, r2, "w2", 1); len(got) != 0 {
		t.Fatalf("granted %v during the startup grace", ids(got))
	}
	// The old holder's epoch is refused even before anyone holds the stream.
	if _, err := appendEvents(r2, "a", l.GetEpoch()-1, event(2, landmarkv1.EventKind_EVENT_KIND_END, 7)); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("epoch below the stored one: %v", err)
	}
	c.advance(ttl)
	got := heartbeat(t, r2, "w2", 1)
	if len(got) != 1 || got[0].GetEpoch() != 2 || got[0].GetNextSeq() != 2 || got[0].GetOpen().GetTrackId() != 7 {
		t.Fatalf("after restart %+v, want epoch 2, next seq 2, open track 7", got)
	}
	b, err := os.ReadFile(filepath.Join(dir, "events", "a.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if b[len(b)-1] != '\n' || slices.Contains(b, '\x00') {
		t.Errorf("torn line not cut: %q", b)
	}
}

func TestAddStreamValidates(t *testing.T) {
	r := open(t, t.TempDir(), &clock{t: time.Unix(1000, 0)})
	tests := []struct {
		id, url string
		code    codes.Code
	}{
		{"radio-1", "http://x.test/1", codes.OK},
		{"radio-1", "http://x.test/1", codes.OK},
		{"radio-1", "http://x.test/other", codes.AlreadyExists},
		{"../etc", "http://x.test/1", codes.InvalidArgument},
		{".hidden", "http://x.test/1", codes.InvalidArgument},
		{"ok", "", codes.InvalidArgument},
	}
	for _, tt := range tests {
		_, err := r.AddStream(context.Background(), &landmarkv1.AddStreamRequest{Stream: &landmarkv1.Stream{Id: tt.id, Url: tt.url}})
		if status.Code(err) != tt.code {
			t.Errorf("AddStream(%q, %q): %v, want %v", tt.id, tt.url, err, tt.code)
		}
	}
}

// serve runs r over an in-memory gRPC connection.
func serve(t *testing.T, r *Registry) landmarkv1.RegistryServiceClient {
	t.Helper()
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
	return landmarkv1.NewRegistryServiceClient(conn)
}

func TestSubscribeReplaysThenFollows(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	r := open(t, t.TempDir(), c)
	addStreams(t, r, "a", "b")
	leases := heartbeat(t, r, "w1", 2)
	if _, err := appendEvents(r, "a", leases[0].GetEpoch(), event(1, landmarkv1.EventKind_EVENT_KIND_START, 7)); err != nil {
		t.Fatal(err)
	}
	client := serve(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sub, err := client.Subscribe(ctx, &landmarkv1.SubscribeRequest{StreamIds: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	recv := func() *landmarkv1.Event {
		t.Helper()
		m, err := sub.Recv()
		if err != nil {
			t.Fatal(err)
		}
		return m.GetEvent()
	}
	if e := recv(); e.GetSeq() != 1 || e.GetStreamId() != "a" || e.GetEpoch() != 1 {
		t.Fatalf("replayed %+v", e)
	}
	// Stream b is filtered out; a's next event arrives live.
	if _, err := appendEvents(r, "b", leases[1].GetEpoch(), event(1, landmarkv1.EventKind_EVENT_KIND_START, 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := appendEvents(r, "a", leases[0].GetEpoch(), event(2, landmarkv1.EventKind_EVENT_KIND_END, 7)); err != nil {
		t.Fatal(err)
	}
	if e := recv(); e.GetSeq() != 2 || e.GetStreamId() != "a" || e.GetKind() != landmarkv1.EventKind_EVENT_KIND_END {
		t.Fatalf("live %+v", e)
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	r := open(t, t.TempDir(), c) // subscriber buffer 4
	addStreams(t, r, "a")
	ep := heartbeat(t, r, "w1", 1)[0].GetEpoch()
	sub := &subscriber{ch: make(chan *landmarkv1.Event, 4)}
	r.mu.Lock()
	r.subs[sub] = struct{}{}
	r.mu.Unlock()
	for seq := uint64(1); seq <= 5; seq++ {
		if _, err := appendEvents(r, "a", ep, event(seq, landmarkv1.EventKind_EVENT_KIND_START, 7)); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for range sub.ch {
		n++
	}
	if n != 4 {
		t.Errorf("received %d before the drop, want 4", n)
	}
	if len(r.subs) != 0 {
		t.Error("dropped subscriber still registered")
	}
}

// TestRenewalNeedsTheCurrentEpoch: a holder from an older grant never
// renews into a newer epoch, which a paused newer holder may still use.
func TestRenewalNeedsTheCurrentEpoch(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	r := open(t, t.TempDir(), c)
	addStreams(t, r, "a")
	w1 := heartbeat(t, r, "w1", 1) // epoch 1
	c.advance(ttl + time.Second)
	heartbeat(t, r, "w2", 1) // epoch 2; then w2 pauses
	c.advance(ttl + time.Second)
	got := heartbeat(t, r, "w1", 1, w1...)
	if len(got) != 1 || got[0].GetEpoch() != 3 {
		t.Fatalf("w1 back with epoch 1 got %+v, want a new grant at epoch 3", got)
	}
}
