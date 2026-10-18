package audio

import (
	"math"
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
