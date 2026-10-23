package audio

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

// TestResamplePolyMatchesScipy compares with scipy.signal.resample_poly
// (defaults) on the same deterministic signal.
func TestResamplePolyMatchesScipy(t *testing.T) {
	x := make([]float32, 300)
	for n := range x {
		v := math.Sin(2*math.Pi*0.03*float64(n)) + 0.5*math.Sin(2*math.Pi*0.21*float64(n)) +
			0.1*(float64((n*7919)%101)/101-0.5)
		x[n] = float32(v)
	}
	tests := []struct {
		up, down, n int
		want        map[int]float64
	}{
		{2, 1, 600, map[int]float64{0: -0.050025876611471176, 1: 0.2714058458805084, 7: 0.12092883139848709, 300: 0.03912914916872978, 599: -0.33243605494499207}},
		{1, 2, 150, map[int]float64{0: 0.17734985053539276, 1: 0.5538844466209412, 7: 0.2804355323314667, 75: 0.019412094727158546, 149: -0.5220571756362915}},
		{51, 50, 306, map[int]float64{0: -0.05003374442458153, 1: 0.65028315782547, 7: 1.1829102039337158, 153: 0.03913530334830284, 305: -0.6745012998580933}},
	}
	for _, tt := range tests {
		y := ResamplePoly(x, tt.up, tt.down)
		if len(y) != tt.n {
			t.Fatalf("%d/%d: %d samples, scipy %d", tt.up, tt.down, len(y), tt.n)
		}
		for i, w := range tt.want {
			if math.Abs(float64(y[i])-w) > 1e-5 {
				t.Errorf("%d/%d: y[%d] = %v, scipy %v", tt.up, tt.down, i, y[i], w)
			}
		}
	}
	if y := ResamplePoly(x, 3, 3); len(y) != len(x) || y[5] != x[5] {
		t.Error("equal rates should copy")
	}
}

// TestResamplerEqualsResamplePoly feeds a signal in uneven pieces and
// checks that the pieces it returns make up ResamplePoly of the whole.
func TestResamplerEqualsResamplePoly(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	x := make([]float32, 20000)
	for i := range x {
		x[i] = float32(rng.NormFloat64())
	}
	for _, ratio := range [][2]int{{2, 1}, {1, 2}, {50, 53}, {3, 3}} {
		r := NewResampler(ratio[0], ratio[1])
		var got []float32
		for pushed := 0; pushed < len(x); {
			n := min(1+rng.IntN(700), len(x)-pushed)
			got = append(got, r.Push(x[pushed:pushed+n])...)
			pushed += n
		}
		got = append(got, r.Close()...)
		want := ResamplePoly(x, ratio[0], ratio[1])
		if !slices.Equal(got, want) {
			t.Errorf("%d/%d: %d samples, ResamplePoly %d; first difference at %d",
				ratio[0], ratio[1], len(got), len(want), firstDiff(got, want))
		}
	}
}

func firstDiff(a, b []float32) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}
