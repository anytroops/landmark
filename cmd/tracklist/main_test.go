package main

import (
	"context"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/audio"
)

func TestWindowStarts(t *testing.T) {
	tests := []struct {
		n, window, hop int
		want           []int
	}{
		{n: 10, window: 4, hop: 3, want: []int{0, 3, 6}},
		{n: 3, window: 4, hop: 3, want: nil},
		{n: 4, window: 4, hop: 1, want: []int{0}},
	}
	for _, tt := range tests {
		if got := windowStarts(tt.n, tt.window, tt.hop); !slices.Equal(got, tt.want) {
			t.Errorf("windowStarts(%d, %d, %d) = %v, want %v", tt.n, tt.window, tt.hop, got, tt.want)
		}
	}
}

func TestRowSkipsTheTopClusterForTheRunnerUp(t *testing.T) {
	cluster := func(d uint32) uint32 {
		if d == 8 {
			return 3 // track 8 duplicates track 3
		}
		return d
	}
	resp := &landmarkv1.IdentifyResponse{Candidates: []*landmarkv1.Candidate{
		{TrackId: 3, Score: 300, Offset: 430, Scale: 1.04},
		{TrackId: 8, Score: 290},
		{TrackId: 5, Score: 12},
	}}
	r := row(7.5, "b2_speed", 2, 1, resp, cluster)
	if r.Track != 3 || r.Score != 300 || r.RunnerUp != 12 || r.Offset != 430 ||
		math.Abs(r.Factor-2.08) > 1e-6 || r.T0 != 7.5 || r.Mode != "b2_speed" {
		t.Fatalf("row = %+v", r)
	}
	pair := row(0, "none", 1, 1.5, &landmarkv1.IdentifyResponse{Candidates: []*landmarkv1.Candidate{
		{TrackId: 3, Score: 100}, {TrackId: 5, Score: 10}}}, cluster)
	if pair.Factor != 1 || pair.Score != 150 || pair.RunnerUp != 15 {
		t.Errorf("weighted pair row = %+v", pair)
	}
	if empty := row(0, "b2", 1, 1, &landmarkv1.IdentifyResponse{}, cluster); empty.Track != -1 || empty.Factor != 1 {
		t.Errorf("no candidates: %+v", empty)
	}
}

func TestSearchWindowsRunsEveryHybridSearch(t *testing.T) {
	pcm := make([]float32, 25*audio.SampleRate)
	state := uint32(7)
	for i := range pcm { // seeded noise: plenty of peaks
		state = state*1664525 + 1013904223
		pcm[i] = float32(state)/(1<<32) - 0.5
	}
	var mu sync.Mutex
	kinds := map[string]int{}
	b2 := func(_ context.Context, req *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
		n := len(req.GetHashes())
		if n == 0 || len(req.GetTimes()) != n || len(req.GetSpans()) != n || len(req.GetBands()) != n {
			t.Errorf("triplet request arrays: %d hashes, %d times, %d spans, %d bands",
				n, len(req.GetTimes()), len(req.GetSpans()), len(req.GetBands()))
		}
		mu.Lock()
		kinds["b2"]++
		mu.Unlock()
		return &landmarkv1.IdentifyResponse{Partial: true,
			Candidates: []*landmarkv1.Candidate{{TrackId: 2, Score: 50, Scale: 1}}}, nil
	}
	pairs := func(_ context.Context, req *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
		if len(req.GetSpans()) != 0 || len(req.GetHashes()) != len(req.GetTimes()) {
			t.Errorf("pair request with %d spans", len(req.GetSpans()))
		}
		mu.Lock()
		kinds["pairs"]++
		mu.Unlock()
		return &landmarkv1.IdentifyResponse{Candidates: []*landmarkv1.Candidate{{TrackId: 2, Score: 40}}}, nil
	}
	s := searcher{cfg: config{window: 10, hop: 5, shifts: 1, workers: 2, timeout: time.Second,
		extra: []float64{2, 0.5}, alpha: 2}, b2: b2, pairs: pairs, cluster: func(d uint32) uint32 { return d }}
	rows, partial, err := s.searchWindows(context.Background(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	// 4 windows (0, 5, 10, 15 s) x (3 triplet + 3 pair searches).
	if len(rows) != 24 || kinds["b2"] != 12 || kinds["pairs"] != 12 || partial != 12 {
		t.Fatalf("%d rows, %v searches, %d partial", len(rows), kinds, partial)
	}
	modes := map[string]int{}
	for _, r := range rows {
		modes[r.Mode]++
		if r.Mode == "none" && r.Score != 80 {
			t.Errorf("pair row not weighted: %+v", r)
		}
	}
	if modes["b2"] != 4 || modes["b2_speed"] != 8 || modes["none"] != 4 || modes["keylock"] != 8 {
		t.Errorf("rows per mode = %v", modes)
	}
}

func TestParseFactors(t *testing.T) {
	got, err := parseFactors("2, 0.5,1.02")
	if err != nil || !slices.Equal(got, []float64{2, 0.5, 1.02}) {
		t.Errorf("parseFactors = %v, %v", got, err)
	}
	if got, err := parseFactors(""); err != nil || len(got) != 0 {
		t.Errorf("empty: %v, %v", got, err)
	}
	for _, bad := range []string{"x", "-1", "0.1234567891"} {
		if _, err := parseFactors(bad); err == nil {
			t.Errorf("parseFactors(%q): want an error", bad)
		}
	}
	if p, q, ok := ratio(0.5); !ok || p != 1 || q != 2 {
		t.Errorf("ratio(0.5) = %d/%d %v", p, q, ok)
	}
}

func TestClock(t *testing.T) {
	for in, want := range map[float64]string{0: "0:00", 59.6: "1:00", 125: "2:05", 3600: "60:00"} {
		if got := clock(in); got != want {
			t.Errorf("clock(%v) = %q, want %q", in, got, want)
		}
	}
}
