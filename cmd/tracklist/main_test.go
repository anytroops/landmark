package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/analysis"
	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/hybrid"
	"github.com/anytroops/landmark/internal/tracklist"
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
		a    analysis.SongReport
		want string
	}{
		{analysis.SongReport{FadeIn: []float64{33.4, 41.1}, FadeOut: []float64{66, 74}, Rate: 1, BPM: 122}, "  in 0:33-0:41  out 1:06-1:14  122.0 BPM"},
		{analysis.SongReport{Rate: 1.04, Turntable: true}, "  turntable"},
		{analysis.SongReport{Rate: 0.97}, "  keylock"},
	}
	for _, tt := range tests {
		if got := describe(tt.a); got != tt.want {
			t.Errorf("describe(%+v) = %q, want %q", tt.a, got, tt.want)
		}
	}
}

func TestWriteRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows.csv")
	rows := []tracklist.Row{
		{T0: 0, Mode: "b2", Factor: 1.0001362562179565, Track: 23986, Score: 668, RunnerUp: 109, Offset: 83},
		{T0: 2.5, Mode: "keylock", Factor: 0.5, Track: -1},
	}
	if err := writeRows(path, rows); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "t0,mode,factor,top1_dense,score,runner_up,offset\n" +
		"0,b2,1.0001362562179565,23986,668,109,83\n" +
		"2.5,keylock,0.5,-1,0,0,0\n"
	if string(b) != want {
		t.Errorf("wrote\n%s\nwant\n%s", b, want)
	}
}

func TestPlaceStarts(t *testing.T) {
	tests := []struct {
		starts                     []int
		offset, grid, dither, last int
		want                       []int
	}{
		{starts: []int{0, 27562, 55124}, grid: 128, last: 1 << 20, want: []int{0, 27648, 55168}},
		{starts: []int{0, 100, 200}, grid: 128, last: 200, want: []int{0, 128}},
		{starts: []int{0, 128}, grid: 128, last: 1000, want: []int{0, 128}},
		{starts: []int{0, 27562}, offset: 64, last: 1 << 20, want: []int{64, 27626}},
		{starts: []int{0, 27562}, offset: 64, grid: 128, last: 1 << 20, want: []int{128, 27648}},
		{starts: []int{0, 100}, offset: 50, last: 120, want: []int{50}},
		{starts: []int{0, 27562, 55124}, grid: 128, dither: 32, last: 1 << 20, want: []int{0, 27680, 55168}},
	}
	for _, tt := range tests {
		if got := placeStarts(tt.starts, tt.offset, tt.grid, tt.dither, tt.last); !slices.Equal(got, tt.want) {
			t.Errorf("placeStarts(%v, %d, %d, %d, %d) = %v, want %v", tt.starts, tt.offset, tt.grid, tt.dither, tt.last, got, tt.want)
		}
	}
}
