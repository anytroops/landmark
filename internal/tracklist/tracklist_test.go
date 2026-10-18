package tracklist

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type golden struct {
	Source       string  `json:"source"`
	WindowS      float64 `json:"window_s"`
	SmoothParams struct {
		TAbs   float64 `json:"t_abs"`
		TRatio float64 `json:"t_ratio"`
		K      int     `json:"k"`
	} `json:"smooth_params"`
	AlignParams struct {
		MinEvidence float64 `json:"min_evidence"`
		Switch      float64 `json:"switch"`
	} `json:"align_params"`
	Rows struct {
		T0       []float64 `json:"t0"`
		Mode     []string  `json:"mode"`
		Factor   []float64 `json:"factor"`
		Track    []int     `json:"top1_dense"`
		Score    []float64 `json:"score"`
		RunnerUp []float64 `json:"runner_up"`
		Offset   []float64 `json:"offset"`
	} `json:"rows"`
	Smooth []Entry `json:"smooth"`
	Align  []Entry `json:"align"`
}

func (g golden) rows() []Row {
	r := g.Rows
	out := make([]Row, len(r.T0))
	for i := range out {
		out[i] = Row{T0: r.T0[i], Mode: r.Mode[i], Factor: r.Factor[i], Track: r.Track[i],
			Score: r.Score[i], RunnerUp: r.RunnerUp[i], Offset: r.Offset[i]}
	}
	return out
}

func loadGoldens(t *testing.T) []golden {
	t.Helper()
	files, err := filepath.Glob("../../testdata/golden_tracklist/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden files: %v", err)
	}
	var out []golden
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var g golden
		if err := json.Unmarshal(b, &g); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out = append(out, g)
	}
	return out
}

func sameEntries(t *testing.T, what string, got, want []Entry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d entries, Python %d:\n got %+v\nwant %+v", what, len(got), len(want), got, want)
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.Track != w.Track || g.Start != w.Start || g.End != w.End || g.Evidence != w.Evidence ||
			math.Abs(g.Factor-w.Factor) > 1e-9*math.Abs(w.Factor) {
			t.Errorf("%s entry %d = %+v, Python %+v", what, i, g, w)
		}
	}
}

// TestParity requires the Python decoders' entries: tracks, times, and
// evidence exactly; tempo factors to 1e-9 (the line fit's rounding differs).
func TestParity(t *testing.T) {
	for _, g := range loadGoldens(t) {
		rows := g.rows()
		SortRows(rows)
		sp, ap := g.SmoothParams, g.AlignParams
		sameEntries(t, g.Source+" smooth", Smooth(rows, g.WindowS, sp.TAbs, sp.TRatio, sp.K), g.Smooth)
		sameEntries(t, g.Source+" align", Align(rows, g.WindowS, ap.MinEvidence, ap.Switch), g.Align)
	}
}

func TestRunsAndMajority(t *testing.T) {
	got := runs([]int{-1, 4, 4, -1, 4, 7}, []float64{0, 2.5, 5, 7.5, 10, 12.5}, 10)
	want := []run{{4, 2.5, 15}, {4, 10, 20}, {7, 12.5, 22.5}}
	if len(got) != len(want) {
		t.Fatalf("runs = %+v, want %+v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("run %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	tests := []struct {
		in   []int
		want int
	}{{[]int{3, 3, 9}, 3}, {[]int{9, 3}, 3}, {[]int{-1, 5, 5, -1}, -1}, {[]int{8}, 8}}
	for _, tt := range tests {
		if m := majority(tt.in); m != tt.want {
			t.Errorf("majority(%v) = %d, want %d", tt.in, m, tt.want)
		}
	}
}

func TestAlignRecoversASpeedChangedPlay(t *testing.T) {
	// Track 5 plays from reference 30 s at 1.05x for 40 s; one stray window
	// names track 9.
	var rows []Row
	for t0 := 0.0; t0 <= 30; t0 += 2.5 {
		rows = append(rows, Row{T0: t0, Mode: "b2", Factor: 1.05, Track: 5, Score: 200,
			RunnerUp: 3, Offset: (30 + 1.05*t0) * FramesPerSecond})
	}
	rows[4].Track, rows[4].Score, rows[4].Offset = 9, 40, 1000
	SortRows(rows)
	got := Align(rows, 10, 50, 4) // switching out and back (2 x 4) costs more than one unknown window earns
	if len(got) != 1 || got[0].Track != 5 || got[0].Start != 0 || got[0].End != 40 ||
		math.Abs(got[0].Factor-1.05) > 1e-9 {
		t.Fatalf("Align = %+v, want one entry for track 5 over 0-40 s at 1.05", got)
	}
}

func TestMergeRepeats(t *testing.T) {
	in := []Entry{
		{Track: 1, Start: 0, End: 42, Factor: 1, Evidence: 900},
		{Track: 2, Start: 40, End: 65, Factor: 0.977, Evidence: 400},
		{Track: 2, Start: 60, End: 70, Factor: 0.977, Evidence: 400},
		{Track: 2, Start: 72, End: 80, Factor: 0.98, Evidence: 500}, // 2 s gap: joined at gap 2.5
		{Track: 3, Start: 70, End: 107, Factor: 1.016, Evidence: 800},
		{Track: 2, Start: 100, End: 110, Factor: 0.977, Evidence: 50}, // after another track: kept
	}
	got := MergeRepeats(in, 2.5)
	want := []Entry{
		{Track: 1, Start: 0, End: 42, Factor: 1, Evidence: 900},
		{Track: 2, Start: 40, End: 80, Factor: 0.98, Evidence: 500},
		{Track: 3, Start: 70, End: 107, Factor: 1.016, Evidence: 800},
		{Track: 2, Start: 100, End: 110, Factor: 0.977, Evidence: 50},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("MergeRepeats =\n%+v\nwant\n%+v", got, want)
	}
}
