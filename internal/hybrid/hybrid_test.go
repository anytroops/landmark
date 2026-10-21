package hybrid

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/audio"
)

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

// noise is seeded noise: plenty of peaks.
func noise(n int) []float32 {
	pcm := make([]float32, n)
	state := uint32(7)
	for i := range pcm {
		state = state*1664525 + 1013904223
		pcm[i] = float32(state)/(1<<32) - 0.5
	}
	return pcm
}

func TestWindowRunsEveryHybridSearch(t *testing.T) {
	kinds := map[string]int{}
	b2 := func(_ context.Context, req *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
		n := len(req.GetHashes())
		if n == 0 || len(req.GetTimes()) != n || len(req.GetSpans()) != n || len(req.GetBands()) != n {
			t.Errorf("triplet request arrays: %d hashes, %d times, %d spans, %d bands",
				n, len(req.GetTimes()), len(req.GetSpans()), len(req.GetBands()))
		}
		kinds["b2"]++
		return &landmarkv1.IdentifyResponse{Partial: true,
			Candidates: []*landmarkv1.Candidate{{TrackId: 2, Score: 50, Scale: 1}}}, nil
	}
	pairs := func(_ context.Context, req *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
		if len(req.GetSpans()) != 0 || len(req.GetHashes()) != len(req.GetTimes()) {
			t.Errorf("pair request with %d spans", len(req.GetSpans()))
		}
		kinds["pairs"]++
		return &landmarkv1.IdentifyResponse{Candidates: []*landmarkv1.Candidate{{TrackId: 2, Score: 40}}}, nil
	}
	s := &Searcher{B2: b2, Pairs: pairs, Cluster: func(d uint32) uint32 { return d }, Shifts: 1,
		Extra: []float64{2, 0.5}, Alpha: 2, Timeout: time.Second}
	rows, partial, err := s.Window(context.Background(), noise(10*audio.SampleRate), 5, NewFingerprinters())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 6 || kinds["b2"] != 3 || kinds["pairs"] != 3 || partial != 3 {
		t.Fatalf("%d rows, %v searches, %d partial", len(rows), kinds, partial)
	}
	var modes []string
	for _, r := range rows {
		modes = append(modes, r.Mode)
		if r.T0 != 5 {
			t.Errorf("row at %v, want 5", r.T0)
		}
		if r.Mode == "none" && r.Score != 80 {
			t.Errorf("pair row not weighted: %+v", r)
		}
	}
	if want := []string{"b2", "b2_speed", "b2_speed", "none", "keylock", "keylock"}; !slices.Equal(modes, want) {
		t.Errorf("modes %v, want %v", modes, want)
	}

	s.Pairs = nil
	if rows, _, err := s.Window(context.Background(), noise(10*audio.SampleRate), 0, NewFingerprinters()); err != nil || len(rows) != 3 {
		t.Errorf("triplet index only: %d rows, %v", len(rows), err)
	}
}

func TestParseFactors(t *testing.T) {
	got, err := ParseFactors("2, 0.5,1.02")
	if err != nil || !slices.Equal(got, []float64{2, 0.5, 1.02}) {
		t.Errorf("ParseFactors = %v, %v", got, err)
	}
	if got, err := ParseFactors(""); err != nil || len(got) != 0 {
		t.Errorf("empty: %v, %v", got, err)
	}
	for _, bad := range []string{"x", "-1", "0.1234567891"} {
		if _, err := ParseFactors(bad); err == nil {
			t.Errorf("ParseFactors(%q): want an error", bad)
		}
	}
	if p, q, ok := Ratio(0.5); !ok || p != 1 || q != 2 {
		t.Errorf("Ratio(0.5) = %d/%d %v", p, q, ok)
	}
}

func TestWindowT0(t *testing.T) {
	if got := WindowT0(27562); got != 2.5 { // 2.49995 s
		t.Errorf("WindowT0(27562) = %v, want 2.5", got)
	}
}
