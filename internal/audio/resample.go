package audio

import "math"

// ResamplePoly changes x's sample rate by up/down, as scipy.signal.resample_poly
// does with its defaults: a lowpass FIR of 20 * max(up, down) + 1 taps
// (cutoff at the lower Nyquist, Kaiser window, beta 5, unity DC gain times
// up), zero padding at the edges, and the output aligned to the input's
// first sample. Output length is ceil(len(x) * up / down). The tempo undo
// of eval.degrade.speed uses it with up/down from the speed factor.
func ResamplePoly(x []float32, up, down int) []float32 {
	g := gcd(up, down)
	up, down = up/g, down/g
	if up == down {
		return append([]float32(nil), x...)
	}
	maxRate := max(up, down)
	half := 10 * maxRate
	h := lowpass(2*half+1, 1/float64(maxRate), float64(up))
	y := make([]float32, (len(x)*up+down-1)/down)
	for m := range y {
		c := m*down + half // upsampled-input index under tap 0
		acc := 0.0
		for j := c % up; j < len(h); j += up {
			i := (c - j) / up
			if i < 0 {
				break
			}
			if i < len(x) {
				acc += h[j] * float64(x[i])
			}
		}
		y[m] = float32(acc)
	}
	return y
}

// lowpass is scipy.signal.firwin(n, cutoff, window=("kaiser", 5.0)) times gain.
func lowpass(n int, cutoff, gain float64) []float64 {
	const beta = 5.0
	h := make([]float64, n)
	alpha := float64(n-1) / 2
	sum := 0.0
	for i := range h {
		m := float64(i) - alpha
		r := m / alpha
		w := besselI0(beta*math.Sqrt(1-r*r)) / besselI0(beta)
		h[i] = cutoff * sinc(cutoff*m) * w
		sum += h[i]
	}
	for i := range h {
		h[i] *= gain / sum
	}
	return h
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	return math.Sin(math.Pi*x) / (math.Pi * x)
}

// besselI0 is the modified Bessel function of the first kind, order 0, by
// its power series (converges fast for the arguments a Kaiser window uses).
func besselI0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1; k < 50; k++ {
		term *= (x / (2 * float64(k))) * (x / (2 * float64(k)))
		sum += term
		if term < 1e-17*sum {
			break
		}
	}
	return sum
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
