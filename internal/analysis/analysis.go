// Package analysis estimates how each song in a DJ mix was mixed in (D43):
// its gain over time, read as a fade-in and a fade-out, its playback rate,
// and whether a tempo change was made like a turntable (pitch moves) or with
// keylock. It ports eval/dj_analysis.py, which chose its methods on the
// UnmixDB calibration sets (EVAL, "DJ mix analysis").
//
// Each song's reference is warped onto the mix's timeline on the triplet
// front end's log-frequency axis (D33): in time by the song's alignment
// (reference seconds = A + Rate * mix seconds), and in pitch by 36 log2(Rate)
// bands if that fits the mix better (a turntable change). Then, frame by
// frame, the mix's band powers are explained as a non-negative combination
// of the songs playing there:
//
//	P_mix(band, t) ~ sum_s g_s(t)^2 P_s(band, t)
//
// and g_s is song s's gain, normalized by its median where it plays alone,
// smoothed, and fitted with a linear ramp at each end.
package analysis

import (
	"math"
	"slices"

	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/tracklist"
)

const (
	// FramesPerSecond is the triplet front end's frame rate (hop 256 at
	// 11,025 Hz).
	FramesPerSecond = 11025.0 / 256
	// margin bands are left out at each end of the axis when comparing or
	// fitting: a pitch shift moves content across the edges, and the lowest
	// bands hold much of a club track's power (+/-8 bands: a 16% turntable
	// change).
	margin         = 8
	bandsPerOctave = 36
	window         = 10.0 // seconds per tracklist window
	reach          = 10.0 // a song may play this long before or after its entry
	silent         = 0.05 // fraction of a song's typical power below which it is not present
	rampStep       = 0.1  // seconds, the ramp fit's grid
	// Gain smoothing, in frames: a median over 0.5 s (21, made odd as
	// int(fps * 0.5) | 1 is in the prototype), then a mean over 1 s (43).
	medianFrames  = 21
	averageFrames = 43
)

// Spectrum is band power over time: Frames x Bands, row-major.
type Spectrum struct {
	Frames, Bands int
	P             []float64
}

// Power converts a log-magnitude spectrogram (dB, triplet.Spectrogram) to
// band powers.
func Power(s fingerprint.Spectrogram) Spectrum {
	p := make([]float64, len(s.Data))
	for i, db := range s.Data {
		p[i] = math.Pow(10, db/10)
	}
	return Spectrum{Frames: s.Frames, Bands: s.Bins, P: p}
}

func (s Spectrum) row(t int) []float64 { return s.P[t*s.Bands : (t+1)*s.Bands] }

// Song is one tracklist entry to analyze.
type Song struct {
	Track      int
	Start, End float64 // the entry, mix seconds
	A, Rate    float64 // reference seconds = A + Rate * mix seconds
	Ref        Spectrum
}

// Result is one song's analysis.
type Result struct {
	Song      int // index into Analyze's songs
	Track     int
	Rate      float64
	Turntable bool // the tempo change moved the pitch too
	// Fades, mix seconds; ok false when the gain curve gave no fit there.
	FadeIn, FadeOut     [2]float64
	FadeInOK, FadeOutOK bool
	Gain                []float64 // normalized gain per frame (1: its level playing alone)
}

// Alignment estimates a song's alignment from its triplet rows (the window
// searched as is) inside its entry: the median rate, then the median
// intercept. ok is false without such rows.
func Alignment(rows []tracklist.Row, track int, start, end float64) (a, rate float64, ok bool) {
	var rates []float64
	var hits []tracklist.Row
	for _, r := range rows {
		if r.Mode == "b2" && r.Track == track && r.T0 >= start-window && r.T0 <= end {
			rates, hits = append(rates, r.Factor), append(hits, r)
		}
	}
	if len(hits) == 0 {
		return 0, 0, false
	}
	rate = median(rates)
	as := make([]float64, len(hits))
	for i, r := range hits {
		as[i] = r.Offset/FramesPerSecond - rate*r.T0
	}
	return median(as), rate, true
}

// warp returns the reference's band powers at each of n mix frames: time
// warped by the alignment (linear interpolation, zero outside the
// reference) and, if pitch, shifted by 36 log2(rate) bands (clamped at the
// edges).
func warp(ref Spectrum, a, rate float64, n int, pitch bool) Spectrum {
	out := Spectrum{Frames: n, Bands: ref.Bands, P: make([]float64, n*ref.Bands)}
	shift := bandsPerOctave * math.Log2(rate)
	tmp := make([]float64, ref.Bands)
	for t := range n {
		pos := (a + rate*(float64(t)/FramesPerSecond)) * FramesPerSecond
		i0 := int(math.Floor(pos))
		if i0 < 0 || i0+1 >= ref.Frames {
			continue
		}
		w := pos - float64(i0)
		r0, r1 := ref.row(i0), ref.row(i0+1)
		row := out.row(t)
		for b := range row {
			row[b] = (1-w)*r0[b] + w*r1[b]
		}
		if !pitch {
			continue
		}
		copy(tmp, row)
		for b := range row {
			row[b] = interp(float64(b)-shift, tmp)
		}
	}
	return out
}

// interp is numpy.interp at x over samples at 0, 1, ...: linear between
// them, clamped to the end values outside.
func interp(x float64, f []float64) float64 {
	switch {
	case x <= 0:
		return f[0]
	case x >= float64(len(f)-1):
		return f[len(f)-1]
	}
	i := int(math.Floor(x))
	w := x - float64(i)
	return (1-w)*f[i] + w*f[i+1]
}

// fitError is how badly a warped reference explains the mix where its entry
// says it plays: the mean squared log10 difference after the best overall
// scale, on the inner bands.
func fitError(mix, w Spectrum, from, to int) float64 {
	var num, den float64
	for t := from; t < to; t++ {
		m, s := mix.row(t)[margin:mix.Bands-margin], w.row(t)[margin:w.Bands-margin]
		for b := range m {
			num += m[b] * s[b]
			den += s[b] * s[b]
		}
	}
	scale := num / math.Max(den, 1e-20)
	sum, n := 0.0, 0
	for t := from; t < to; t++ {
		m, s := mix.row(t)[margin:mix.Bands-margin], w.row(t)[margin:w.Bands-margin]
		for b := range m {
			d := math.Log10(m[b]+1e-12) - math.Log10(scale*s[b]+1e-12)
			sum += d * d
			n++
		}
	}
	if n == 0 {
		return math.Inf(1)
	}
	return sum / float64(n)
}

// Analyze estimates every song's gain curve and fades in a mix.
func Analyze(mix Spectrum, songs []Song) []Result {
	n := mix.Frames
	warped := make([]Spectrum, len(songs))
	turntable := make([]bool, len(songs))
	for k, s := range songs {
		from := int(s.Start * FramesPerSecond)
		to := int(math.Min(s.End, float64(n)/FramesPerSecond) * FramesPerSecond)
		best := math.Inf(1)
		for _, pitch := range []bool{false, true} {
			w := warp(s.Ref, s.A, s.Rate, n, pitch)
			if e := fitError(mix, w, from, to); e < best {
				best, warped[k], turntable[k] = e, w, pitch
			}
		}
	}
	active := make([][]bool, len(songs)) // [song][frame]
	for k, s := range songs {
		active[k] = present(warped[k], s, n)
	}
	g := gains(mix, warped, active)
	for k := range g {
		g[k] = movingAverage(medianFilter(g[k], medianFrames), averageFrames)
	}

	var out []Result
	for k, s := range songs {
		var alone []float64
		for t := range n {
			if active[k][t] && count(active, t) == 1 {
				alone = append(alone, g[k][t])
			}
		}
		level := slices.Max(append([]float64{0}, g[k]...))
		if len(alone) > 0 {
			level = median(alone)
		}
		if level <= 0 {
			continue
		}
		x := make([]float64, n)
		for t := range n {
			x[t] = g[k][t] / level
		}
		r := Result{Song: k, Track: s.Track, Rate: s.Rate, Turntable: turntable[k], Gain: x}
		last := float64(n-1) / FramesPerSecond
		r.FadeIn, r.FadeInOK = fitRamp(x, true, math.Max(0, s.Start-5), s.Start+15)
		r.FadeOut, r.FadeOutOK = fitRamp(x, false, s.End-15, math.Min(last, s.End+5))
		out = append(out, r)
	}
	return out
}

// present marks the frames where a song may play: within reach of its
// entry, and where its warped reference has real power (a near-silent
// stretch, such as an excerpt's ends, would take any gain).
func present(w Spectrum, s Song, n int) []bool {
	lo, hi := math.Max(0, s.Start-reach), math.Min(float64(n)/FramesPerSecond, s.End+reach)
	power := make([]float64, n)
	var inside []float64
	for t := range n {
		for _, v := range w.row(t) {
			power[t] += v
		}
		if tt := float64(t) / FramesPerSecond; tt >= lo && tt <= hi && power[t] > 0 {
			inside = append(inside, power[t])
		}
	}
	typical := 0.0
	if len(inside) > 0 {
		typical = median(inside)
	}
	out := make([]bool, n)
	for t := range n {
		tt := float64(t) / FramesPerSecond
		out[t] = tt >= lo && tt <= hi && power[t] > silent*typical
	}
	return out
}

func count(active [][]bool, t int) int {
	c := 0
	for _, a := range active {
		if a[t] {
			c++
		}
	}
	return c
}

// gains solves, per frame, the non-negative least squares of the mix's
// inner band powers on the active songs' and returns amplitudes
// [song][frame].
func gains(mix Spectrum, warped []Spectrum, active [][]bool) [][]float64 {
	g := make([][]float64, len(warped))
	for k := range g {
		g[k] = make([]float64, mix.Frames)
	}
	cols := make([][]float64, 0, len(warped))
	for t := range mix.Frames {
		var idx []int
		cols = cols[:0]
		for k := range warped {
			if active[k][t] {
				idx = append(idx, k)
				cols = append(cols, warped[k].row(t)[margin:mix.Bands-margin])
			}
		}
		if len(idx) == 0 {
			continue
		}
		coef := nnls(cols, mix.row(t)[margin:mix.Bands-margin])
		for i, k := range idx {
			g[k][t] = math.Sqrt(coef[i])
		}
	}
	return g
}

// nnls minimizes ||sum_i c_i cols_i - y|| over c >= 0 exactly for the few
// columns here (a frame has at most a handful of songs): the optimum is the
// unconstrained least squares on some subset of columns with every
// coefficient non-negative, so every subset is tried.
func nnls(cols [][]float64, y []float64) []float64 {
	k := len(cols)
	best, bestErr := make([]float64, k), math.Inf(1)
	for mask := 0; mask < 1<<k; mask++ {
		var sub []int
		for i := range k {
			if mask&(1<<i) != 0 {
				sub = append(sub, i)
			}
		}
		c, ok := leastSquares(cols, sub, y)
		if !ok {
			continue
		}
		full := make([]float64, k)
		for j, i := range sub {
			full[i] = c[j]
		}
		if e := residual(cols, full, y); e < bestErr {
			best, bestErr = full, e
		}
	}
	return best
}

// leastSquares solves the normal equations for the columns in sub; ok is
// false if a coefficient is negative or the system is singular.
func leastSquares(cols [][]float64, sub []int, y []float64) ([]float64, bool) {
	m := len(sub)
	if m == 0 {
		return nil, true
	}
	a := make([][]float64, m) // augmented normal equations
	for r, i := range sub {
		a[r] = make([]float64, m+1)
		for c, j := range sub {
			a[r][c] = dot(cols[i], cols[j])
		}
		a[r][m] = dot(cols[i], y)
	}
	for c := range m { // Gaussian elimination with partial pivoting
		p := c
		for r := c + 1; r < m; r++ {
			if math.Abs(a[r][c]) > math.Abs(a[p][c]) {
				p = r
			}
		}
		if a[p][c] == 0 {
			return nil, false
		}
		a[c], a[p] = a[p], a[c]
		for r := c + 1; r < m; r++ {
			f := a[r][c] / a[c][c]
			for j := c; j <= m; j++ {
				a[r][j] -= f * a[c][j]
			}
		}
	}
	x := make([]float64, m)
	for r := m - 1; r >= 0; r-- {
		s := a[r][m]
		for j := r + 1; j < m; j++ {
			s -= a[r][j] * x[j]
		}
		x[r] = s / a[r][r]
		if x[r] < 0 {
			return nil, false
		}
	}
	return x, true
}

func residual(cols [][]float64, c, y []float64) float64 {
	e := 0.0
	for b := range y {
		v := -y[b]
		for i, col := range cols {
			v += c[i] * col[b]
		}
		e += v * v
	}
	return e
}

func dot(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// reflect maps an index past either end back inside as scipy.ndimage's
// "reflect" mode does (d c b a | a b c d | d c b a).
func reflect(i, n int) int {
	for i < 0 || i >= n {
		if i < 0 {
			i = -i - 1
		} else {
			i = 2*n - i - 1
		}
	}
	return i
}

// medianFilter is scipy.ndimage.median_filter along one axis (odd size,
// reflect mode).
func medianFilter(x []float64, size int) []float64 {
	out := make([]float64, len(x))
	w := make([]float64, size)
	for i := range x {
		for j := range size {
			w[j] = x[reflect(i-size/2+j, len(x))]
		}
		slices.Sort(w)
		out[i] = w[size/2]
	}
	return out
}

// movingAverage is scipy.ndimage.uniform_filter1d (reflect mode); an even
// size's window reaches one sample further left, as scipy's does.
func movingAverage(x []float64, size int) []float64 {
	out := make([]float64, len(x))
	for i := range x {
		s := 0.0
		for j := range size {
			s += x[reflect(i-size/2+j, len(x))]
		}
		out[i] = s / float64(size)
	}
	return out
}

// fitRamp finds the linear fade (0 to 1 if rising, 1 to 0 if not, flat
// outside) that best fits the normalized gain x by least squares, its start
// and end on a 0.1 s grid within [lo, hi].
func fitRamp(x []float64, rising bool, lo, hi float64) ([2]float64, bool) {
	var ts, xs []float64
	for t, v := range x {
		if tt := float64(t) / FramesPerSecond; tt >= lo && tt <= hi {
			ts, xs = append(ts, tt), append(xs, math.Min(math.Max(v, 0), 1.5))
		}
	}
	if len(ts) < 2 {
		return [2]float64{}, false
	}
	steps := int(math.Ceil((hi + 1e-9 - lo) / rampStep)) // numpy.arange's length
	grid := make([]float64, steps)
	for i := range grid {
		grid[i] = lo + float64(i)*rampStep
	}
	best, bestErr := [2]float64{}, math.Inf(1)
	for i, a := range grid {
		for _, b := range grid[i:] {
			e := 0.0
			for j, tt := range ts {
				r := math.Min(math.Max((tt-a)/math.Max(b-a, 1e-9), 0), 1)
				if !rising {
					r = 1 - r
				}
				d := xs[j] - r
				e += d * d
			}
			if e < bestErr {
				best, bestErr = [2]float64{a, b}, e
			}
		}
	}
	return best, true
}

func median(v []float64) float64 {
	s := slices.Sorted(slices.Values(v))
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}
