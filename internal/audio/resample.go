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

// Resampler is ResamplePoly for a signal that arrives in pieces (D48): the
// pieces it returns, concatenated, equal ResamplePoly of the whole signal
// sample for sample. An output sample comes out once every input sample
// under its filter has arrived; Close produces the rest, with zeros past the
// end, as ResamplePoly pads.
type Resampler struct {
	up, down, half int
	h              []float64
	in             []float32 // input from inStart on
	inStart, n     int       // n: input samples pushed
	m              int       // next output sample
}

// NewResampler returns a Resampler by up/down.
func NewResampler(up, down int) *Resampler {
	g := gcd(up, down)
	r := &Resampler{up: up / g, down: down / g}
	if r.up != r.down {
		maxRate := max(r.up, r.down)
		r.half = 10 * maxRate
		r.h = lowpass(2*r.half+1, 1/float64(maxRate), float64(r.up))
	}
	return r
}

// Push adds input and returns the output samples it completes.
func (r *Resampler) Push(x []float32) []float32 {
	if r.up == r.down {
		r.n += len(x)
		return append([]float32(nil), x...)
	}
	r.in = append(r.in, x...)
	r.n += len(x)
	return r.produce(false)
}

// Close ends the input and returns the remaining output.
func (r *Resampler) Close() []float32 {
	if r.up == r.down {
		return nil
	}
	return r.produce(true)
}

func (r *Resampler) produce(end bool) []float32 {
	var out []float32
	total := (r.n*r.up + r.down - 1) / r.down
	for {
		c := r.m*r.down + r.half // upsampled-input index under tap 0, as ResamplePoly
		if (end && r.m >= total) || (!end && c/r.up >= r.n) {
			break
		}
		acc := 0.0
		for j := c % r.up; j < len(r.h); j += r.up {
			i := (c - j) / r.up
			if i < 0 {
				break
			}
			if i < r.n {
				acc += r.h[j] * float64(r.in[i-r.inStart])
			}
		}
		out = append(out, float32(acc))
		r.m++
	}
	// The next output's filter reaches back to (c - (len(h) - 1)) / up.
	if keep := (r.m*r.down+r.half-len(r.h)+1)/r.up - 1; keep > r.inStart {
		r.in = append(r.in[:0], r.in[keep-r.inStart:]...)
		r.inStart = keep
	}
	return out
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
