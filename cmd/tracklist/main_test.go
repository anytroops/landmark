package main

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/hybrid"
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
	s := searcher{cfg: config{window: 10, hop: 5, workers: 2},
		hybrid: &hybrid.Searcher{B2: b2, Pairs: pairs, Cluster: func(d uint32) uint32 { return d }, Shifts: 1,
			Extra: []float64{2, 0.5}, Alpha: 2, Timeout: time.Second}}
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

func TestClock(t *testing.T) {
	for in, want := range map[float64]string{0: "0:00", 59.6: "1:00", 125: "2:05", 3600: "60:00"} {
		if got := clock(in); got != want {
			t.Errorf("clock(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestDescribe(t *testing.T) {
	tests := []struct {
		a    songAnalysis
		want string
	}{
		{songAnalysis{FadeIn: []float64{33.4, 41.1}, FadeOut: []float64{66, 74}, Rate: 1, BPM: 122}, "  in 0:33-0:41  out 1:06-1:14  122.0 BPM"},
		{songAnalysis{Rate: 1.04, Turntable: true}, "  turntable"},
		{songAnalysis{Rate: 0.97}, "  keylock"},
	}
	for _, tt := range tests {
		if got := describe(tt.a); got != tt.want {
			t.Errorf("describe(%+v) = %q, want %q", tt.a, got, tt.want)
		}
	}
}
