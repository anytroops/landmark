package analysis

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/anytroops/landmark/internal/tracklist"
)

func TestReflectMatchesScipy(t *testing.T) {
	// scipy.ndimage "reflect": (d c b a | a b c d | d c b a)
	n := 4
	got := []int{}
	for i := -4; i < 8; i++ {
		got = append(got, reflect(i, n))
	}
	if want := []int{3, 2, 1, 0, 0, 1, 2, 3, 3, 2, 1, 0}; !slices.Equal(got, want) {
		t.Errorf("reflect = %v, want %v", got, want)
	}
}

func TestFilters(t *testing.T) {
	x := []float64{5, 1, 9, 3, 7}
	// scipy.ndimage.median_filter(x, 3) -> [5, 5, 3, 7, 7] (window at 0: 5 5 1)
	if got := medianFilter(x, 3); !slices.Equal(got, []float64{5, 5, 3, 7, 7}) {
		t.Errorf("medianFilter = %v", got)
	}
	// scipy.ndimage.uniform_filter1d(x, 3) -> [11/3, 5, 13/3, 19/3, 17/3]
	got := movingAverage(x, 3)
	for i, w := range []float64{11.0 / 3, 5, 13.0 / 3, 19.0 / 3, 17.0 / 3} {
		if math.Abs(got[i]-w) > 1e-12 {
			t.Errorf("movingAverage[%d] = %v, want %v", i, got[i], w)
		}
	}
}

func TestNNLS(t *testing.T) {
	a := []float64{1, 0, 1}
	b := []float64{0, 1, 1}
	tests := []struct {
		name string
		y    []float64
		want []float64
	}{
		{"both positive", []float64{2, 3, 5}, []float64{2, 3}},
		// Unconstrained: b's coefficient negative; the optimum drops b.
		{"one clipped", []float64{2, -1, 1}, []float64{1.5, 0}},
		{"none", []float64{-1, -1, -2}, []float64{0, 0}},
	}
	for _, tt := range tests {
		got := nnls([][]float64{a, b}, tt.y)
		for i := range got {
			if math.Abs(got[i]-tt.want[i]) > 1e-9 {
				t.Errorf("%s: nnls = %v, want %v", tt.name, got, tt.want)
			}
		}
	}
}

func TestFitRampFindsALinearFade(t *testing.T) {
	fps := FramesPerSecond
	n := int(30 * fps)
	x := make([]float64, n)
	for i := range x {
		tt := float64(i) / FramesPerSecond
		x[i] = math.Min(math.Max((tt-8)/6, 0), 1) // fades in from 8 to 14 s
	}
	got, ok := fitRamp(x, true, 3, 20)
	if !ok || math.Abs(got[0]-8) > 0.11 || math.Abs(got[1]-14) > 0.11 {
		t.Fatalf("fade in %v (%v), want about [8 14]", got, ok)
	}
	for i := range x {
		x[i] = 1 - x[i]
	}
	if got, ok := fitRamp(x, false, 3, 20); !ok || math.Abs(got[0]-8) > 0.11 || math.Abs(got[1]-14) > 0.11 {
		t.Fatalf("fade out %v (%v), want about [8 14]", got, ok)
	}
}

// randomSong makes a reference with a distinct band profile and some
// variation over time.
func randomSong(rng *rand.Rand, frames, bands int) Spectrum {
	profile := make([]float64, bands)
	for b := range profile {
		profile[b] = math.Pow(10, rng.Float64()*3)
	}
	s := Spectrum{Frames: frames, Bands: bands, P: make([]float64, frames*bands)}
	for t := range frames {
		row := s.row(t)
		for b := range row {
			row[b] = profile[b] * (0.5 + rng.Float64())
		}
	}
	return s
}

// TestAnalyzeRecoversACrossfade mixes two synthetic songs with a linear
// crossfade, the second sped up 4% like a turntable, and requires the fades
// and the kind of tempo change back.
func TestAnalyzeRecoversACrossfade(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	const bands = 80
	fps := FramesPerSecond
	n := int(60 * fps)
	a, b := randomSong(rng, int(50*fps), bands), randomSong(rng, int(50*fps), bands)
	// Song A plays from mix 0 (reference 0, rate 1) and fades out 25-33 s;
	// song B fades in 25-33 s, sped up 4% (reference = 1.04 * (t - 25)).
	wa := warp(a, 0, 1, n, false)
	wb := warp(b, -1.04*25, 1.04, n, true)
	mix := Spectrum{Frames: n, Bands: bands, P: make([]float64, n*bands)}
	for i := range n {
		tt := float64(i) / fps
		ga := math.Min(math.Max((33-tt)/8, 0), 1)
		gb := math.Min(math.Max((tt-25)/8, 0), 1)
		row := mix.row(i)
		for k := range row {
			row[k] = ga*ga*wa.row(i)[k] + gb*gb*wb.row(i)[k]
		}
	}
	res := Analyze(mix, []Song{
		{Track: 1, Start: 0, End: 35, A: 0, Rate: 1, Ref: a},
		{Track: 2, Start: 25, End: 60, A: -1.04 * 25, Rate: 1.04, Ref: b},
	})
	if len(res) != 2 {
		t.Fatalf("%d results", len(res))
	}
	if !res[0].FadeOutOK || math.Abs(res[0].FadeOut[0]-25) > 1 || math.Abs(res[0].FadeOut[1]-33) > 1 {
		t.Errorf("song 1 fade-out %v, want about [25 33]", res[0].FadeOut)
	}
	if !res[1].FadeInOK || math.Abs(res[1].FadeIn[0]-25) > 1 || math.Abs(res[1].FadeIn[1]-33) > 1 {
		t.Errorf("song 2 fade-in %v, want about [25 33]", res[1].FadeIn)
	}
	if !res[1].Turntable || res[0].Turntable {
		t.Errorf("turntable: song 1 %v, song 2 %v; want false, true", res[0].Turntable, res[1].Turntable)
	}
}

func TestAlignment(t *testing.T) {
	// Reference = 3 + 1.02 * mix: a window at t0 starts at reference frame
	// (3 + 1.02 t0) * fps.
	var rows []tracklist.Row
	for _, t0 := range []float64{10, 12.5, 15, 17.5} {
		rows = append(rows, tracklist.Row{T0: t0, Mode: "b2", Track: 4, Factor: 1.02,
			Offset: (3 + 1.02*t0) * FramesPerSecond})
	}
	rows = append(rows, tracklist.Row{T0: 12.5, Mode: "none", Track: 4, Factor: 1, Offset: 0}) // pair rows ignored
	a, rate, ok := Alignment(rows, 4, 12, 20)
	if !ok || math.Abs(a-3) > 1e-9 || rate != 1.02 {
		t.Errorf("alignment %v %v %v, want 3 1.02", a, rate, ok)
	}
	if _, _, ok := Alignment(rows, 9, 0, 100); ok {
		t.Error("a track without rows: want not ok")
	}
}
