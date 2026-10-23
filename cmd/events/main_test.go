package main

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/registry"
)

// syncBuffer is a bytes.Buffer safe to read while follow writes it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestFollowPrintsLoggedThenLiveEvents(t *testing.T) {
	now := time.Unix(1000, 0)
	r, err := registry.Open(registry.Options{Dir: t.TempDir(), TTL: time.Second, Now: func() time.Time { return now }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	now = now.Add(2 * time.Second) // past the startup grace
	ctx := context.Background()
	if _, err := r.AddStream(ctx, &landmarkv1.AddStreamRequest{Stream: &landmarkv1.Stream{Id: "st1", Url: "x"}}); err != nil {
		t.Fatal(err)
	}
	hb, err := r.Heartbeat(ctx, &landmarkv1.HeartbeatRequest{WorkerId: "w", Capacity: 1})
	if err != nil {
		t.Fatal(err)
	}
	epoch := hb.GetLeases()[0].GetEpoch()
	appendEvent := func(e *landmarkv1.Event) {
		t.Helper()
		if _, err := r.Append(ctx, &landmarkv1.AppendRequest{StreamId: "st1", Epoch: epoch, Events: []*landmarkv1.Event{e}}); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent(&landmarkv1.Event{Seq: 1, Kind: landmarkv1.EventKind_EVENT_KIND_START, Name: "Song A", StartUnix: 100, EndUnix: 110, Tempo: 1})

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	landmarkv1.RegisterRegistryServiceServer(gs, r)
	go func() { _ = gs.Serve(lis) }()
	defer gs.Stop()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	var out syncBuffer
	fctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- follow(fctx, landmarkv1.NewRegistryServiceClient(conn), nil, false, false, &out) }()
	wait := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(out.String(), want) {
			if time.Now().After(deadline) {
				t.Fatalf("output %q never contained %q", out.String(), want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait("playing  Song A")
	appendEvent(&landmarkv1.Event{Seq: 2, Kind: landmarkv1.EventKind_EVENT_KIND_END, Name: "Song A", StartUnix: 100, EndUnix: 160, Tempo: 1.04, Resumed: true})
	wait("played   Song A for 60 s (+4.0%) [continued after a failover or reconnect]")
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out.String(), "\n"); n != 2 {
		t.Errorf("%d lines, want 2:\n%s", n, out.String())
	}
}
