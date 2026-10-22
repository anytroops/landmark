package shard

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/segment"
)

// versions writes partition 1 of 2 as a base (testSegment's tracks) and two
// deltas adding local track 4 (global 9, hashes 30..49) and local track 5
// (global 11, hashes 50..69), and returns a function opening version v
// (1: base, 2: base + first delta, 3: both) as fresh segments.
func versions(t *testing.T) func(v uint64) Content {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, ntracks, track, from uint32, extra []segment.Entry) string {
		var entries []segment.Entry
		for h := from; h < from+20; h++ {
			entries = append(entries, segment.Entry{Hash: h, Track: track, T: h})
		}
		path := filepath.Join(dir, name)
		meta := segment.Meta{Partition: 1, NPartitions: 2, NTracks: ntracks, Encoding: segment.EncodingVarint}
		if err := segment.Write(path, meta, append(entries, extra...)); err != nil {
			t.Fatal(err)
		}
		return path
	}
	var base []segment.Entry
	for h := uint32(10); h < 15; h++ {
		base = append(base, segment.Entry{Hash: h, Track: 0, T: h + 100})
	}
	files := []string{write("base", 4, 3, 10, base), write("d1", 5, 4, 30, nil), write("d2", 6, 5, 50, nil)}
	return func(v uint64) Content {
		t.Helper()
		var segs []*segment.Segment
		for _, f := range files[:v] {
			s, err := segment.Open(f)
			if err != nil {
				t.Fatal(err)
			}
			segs = append(segs, s)
		}
		set, err := segment.NewSet(segs)
		if err != nil {
			t.Fatal(err)
		}
		return Content{Set: set, Version: v, Checksum: uint32(100 + v)}
	}
}

// ask searches hashes from..from+19 at t = hash, pinned to version (0: current).
func ask(client landmarkv1.ShardServiceClient, from uint32, version uint64) (*landmarkv1.SearchResponse, error) {
	req := &landmarkv1.SearchRequest{TopK: 5, ManifestVersion: version}
	for h := from; h < from+20; h++ {
		req.Hashes, req.Times = append(req.Hashes, h), append(req.Times, h)
	}
	return client.Search(context.Background(), req)
}

func top(resp *landmarkv1.SearchResponse) uint32 {
	if len(resp.GetCandidates()) == 0 {
		return 0
	}
	return resp.GetCandidates()[0].GetTrackId()
}

func TestSwapServesNewVersions(t *testing.T) {
	open := versions(t)
	srv, err := New(open(1), Config{Workers: 2, MaxQueueWait: time.Second}, prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	client := startServer(t, srv)
	if resp, err := ask(client, 30, 0); err != nil || len(resp.GetCandidates()) != 0 || resp.GetManifestVersion() != 1 {
		t.Fatalf("version 1, added song's hashes: %v, %v; want no match", resp, err)
	}
	if err := srv.Swap(open(2)); err != nil {
		t.Fatal(err)
	}
	if resp, err := ask(client, 30, 0); err != nil || top(resp) != 9 || resp.GetManifestVersion() != 2 {
		t.Fatalf("version 2: %v, %v; want global track 9", resp, err)
	}
	if resp, err := ask(client, 30, 1); err != nil || len(resp.GetCandidates()) != 0 || resp.GetManifestVersion() != 1 {
		t.Fatalf("pinned to version 1 after the swap: %v, %v", resp, err)
	}
	if err := srv.Swap(open(3)); err != nil {
		t.Fatal(err)
	}
	if _, err := ask(client, 30, 1); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("version 1 after two swaps: %v, want FailedPrecondition", err)
	}
	if resp, err := ask(client, 50, 2); err != nil || len(resp.GetCandidates()) != 0 {
		t.Errorf("version 2 must not see version 3's song: %v, %v", resp, err)
	}
	if resp, err := ask(client, 50, 3); err != nil || top(resp) != 11 {
		t.Errorf("version 3: %v, %v; want global track 11", resp, err)
	}
	info, err := client.Info(context.Background(), &landmarkv1.InfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	vs := info.GetVersions()
	if len(vs) != 2 || vs[0].GetVersion() != 3 || vs[1].GetVersion() != 2 || vs[0].GetChecksum() != 103 ||
		info.GetSegmentCrc32C() != 103 || info.GetNTracks() != 6 {
		t.Errorf("Info = %v", info)
	}
	for _, bad := range []uint64{3, 2} {
		if err := srv.Swap(open(bad)); err == nil {
			t.Errorf("swap back to version %d: want an error", bad)
		}
	}
}

func TestRetiredVersionClosesAfterItsLastSearch(t *testing.T) {
	open := versions(t)
	srv, err := New(open(1), Config{Workers: 1, MaxQueueWait: time.Second}, prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	inFlight := srv.hold(0) // a search still running on version 1
	for _, v := range []uint64{2, 3} {
		if err := srv.Swap(open(v)); err != nil {
			t.Fatal(err)
		}
	}
	inFlight.mu.Lock()
	retired, closed := inFlight.retired, inFlight.closed
	inFlight.mu.Unlock()
	if !retired || closed {
		t.Fatalf("version 1 with a search running: retired %v closed %v, want retired, open", retired, closed)
	}
	if _, err := inFlight.Set.AppendPostings(nil, 10); err != nil {
		t.Fatalf("the running search can still read: %v", err)
	}
	inFlight.release()
	if !inFlight.closed {
		t.Error("version 1 not closed after its last search")
	}
}

func TestSearchesAcrossSwaps(t *testing.T) {
	open := versions(t)
	srv, err := New(open(1), Config{Workers: 4, MaxQueueWait: 5 * time.Second}, prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	client := startServer(t, srv)
	stop := make(chan struct{})
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				resp, err := ask(client, 10, 0)
				if err == nil && top(resp) != 7 {
					err = fmt.Errorf("goroutine %d: top %d, want 7", g, top(resp))
				}
				if err != nil {
					errs <- err
					return
				}
			}
		})
	}
	for v := uint64(2); v <= 3; v++ {
		time.Sleep(50 * time.Millisecond)
		if err := srv.Swap(open(v)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
