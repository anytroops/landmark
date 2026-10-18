package main

import (
	"context"
	"math"
	"slices"
	"testing"

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
	r := row(7.5, resp, cluster)
	if r.Track != 3 || r.Score != 300 || r.RunnerUp != 12 || r.Offset != 430 ||
		math.Abs(r.Factor-1.04) > 1e-6 || r.T0 != 7.5 || r.Mode != "b2" {
		t.Fatalf("row = %+v", r)
	}
	if empty := row(0, &landmarkv1.IdentifyResponse{}, cluster); empty.Track != -1 || empty.Factor != 1 {
		t.Errorf("no candidates: %+v", empty)
	}
}

func TestSearchWindowsSendsTripletRequests(t *testing.T) {
	pcm := make([]float32, 25*audio.SampleRate)
	state := uint32(7)
	for i := range pcm { // seeded noise: plenty of peaks
		state = state*1664525 + 1013904223
		pcm[i] = float32(state)/(1<<32) - 0.5
	}
	cfg := config{window: 10, hop: 5, shifts: 1, workers: 2, timeout: 1e9}
	identify := func(_ context.Context, req *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
		n := len(req.GetHashes())
		if n == 0 || len(req.GetTimes()) != n || len(req.GetSpans()) != n || len(req.GetBands()) != n {
			t.Errorf("request arrays: %d hashes, %d times, %d spans, %d bands",
				n, len(req.GetTimes()), len(req.GetSpans()), len(req.GetBands()))
		}
		return &landmarkv1.IdentifyResponse{Partial: true,
			Candidates: []*landmarkv1.Candidate{{TrackId: 2, Score: 50, Scale: 1}}}, nil
	}
	rows, partial, err := searchWindows(context.Background(), cfg, pcm, identify, func(d uint32) uint32 { return d })
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || partial != 4 {
		t.Fatalf("%d rows, %d partial; want 4 windows (0, 5, 10, 15 s), all partial", len(rows), partial)
	}
	for i, r := range rows {
		if r.T0 != float64(5*i) || r.Track != 2 {
			t.Errorf("row %d = %+v", i, r)
		}
	}
}

func TestClock(t *testing.T) {
	for in, want := range map[float64]string{0: "0:00", 59.6: "1:00", 125: "2:05", 3600: "60:00"} {
		if got := clock(in); got != want {
			t.Errorf("clock(%v) = %q, want %q", in, got, want)
		}
	}
}
