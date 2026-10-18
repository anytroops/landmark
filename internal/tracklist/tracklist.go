// Package tracklist turns per-window search results from a DJ mix into a
// tracklist (D32). It ports the decoders of eval/tracklist_score.py, which
// produced the evaluation numbers, and is tested against them
// (testdata/golden_tracklist):
//
//   - Smooth (D32 option i): per window take the best-scoring row, accept it
//     if its score clears tAbs and tRatio times the runner-up, majority-vote
//     each window with its k neighbors on each side, and turn runs of one
//     track into entries.
//   - Align (D32 options ii + iii): fit a line through each candidate
//     track's matches in (mix time, reference time). A real play advances
//     linearly at a steady speed and chance matches do not, so only matches
//     near the line count, and the slope is the speed. Then Viterbi over the
//     windows picks one track or "unknown" per window, rewarding on-line
//     evidence and charging a penalty per switch.
package tracklist

import (
	"cmp"
	"math"
	"slices"
)

// FramesPerSecond converts match offsets (frames) to seconds: hop 256 at
// 11,025 Hz.
const FramesPerSecond = 11025.0 / 256

// Row is one search of one window: the top candidate under one hypothesis
// (the speed grid has many per window; B2 one, or a few with double- and
// half-time pre-stretches).
type Row struct {
	T0       float64 // window start in the mix, seconds
	Mode     string  // hypothesis kind, e.g. "none", "speed", "b2"
	Factor   float64 // tempo factor: reference time per mix time
	Track    int     // top candidate's dense id, or -1 for none
	Score    float64
	RunnerUp float64 // best score outside the top candidate's duplicate cluster
	Offset   float64 // reference frame at the window's first frame
}

// Entry is one tracklist line.
type Entry struct {
	Track    int
	Start    float64 // seconds in the mix
	End      float64
	Factor   float64 // estimated tempo factor (1 = unchanged)
	Evidence float64
}

// SortRows puts rows in the order the decoders expect (and the Python
// reference uses): by window start, then mode, then factor. Ties between
// equal scores go to the first row in this order.
func SortRows(rows []Row) {
	slices.SortStableFunc(rows, func(a, b Row) int {
		return cmp.Or(cmp.Compare(a.T0, b.T0), cmp.Compare(a.Mode, b.Mode), cmp.Compare(a.Factor, b.Factor))
	})
}

// bestPerWindow returns the first highest-scoring row of each window, in
// window order. rows must be sorted (SortRows).
func bestPerWindow(rows []Row) []Row {
	var out []Row
	for i := 0; i < len(rows); {
		j, best := i, i
		for j < len(rows) && rows[j].T0 == rows[i].T0 {
			if rows[j].Score > rows[best].Score {
				best = j
			}
			j++
		}
		out = append(out, rows[best])
		i = j
	}
	return out
}

func accepted(r Row, tAbs, tRatio float64) bool {
	return r.Track >= 0 && r.Score >= tAbs && r.Score >= tRatio*math.Max(r.RunnerUp, 1)
}

type run struct {
	label      int
	start, end float64
}

// runs returns contiguous runs of the same non-negative label.
func runs(labels []int, t0s []float64, window float64) []run {
	var out []run
	for i, lab := range labels {
		if lab < 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].label == lab && labels[i-1] == lab {
			out[n-1].end = t0s[i] + window
		} else {
			out = append(out, run{lab, t0s[i], t0s[i] + window})
		}
	}
	return out
}

// Smooth decodes with option i. rows must be sorted (SortRows).
func Smooth(rows []Row, window, tAbs, tRatio float64, k int) []Entry {
	best := bestPerWindow(rows)
	labels := make([]int, len(best))
	t0s := make([]float64, len(best))
	for i, r := range best {
		labels[i], t0s[i] = -1, r.T0
		if accepted(r, tAbs, tRatio) {
			labels[i] = r.Track
		}
	}
	smoothed := make([]int, len(labels))
	for i := range labels {
		smoothed[i] = majority(labels[max(0, i-k):min(len(labels), i+k+1)])
	}
	var out []Entry
	for _, r := range runs(smoothed, t0s, window) {
		var factors []float64
		evidence := 0.0
		for _, b := range best {
			if b.T0 >= r.start && b.T0+window <= r.end && b.Track == r.label {
				factors = append(factors, b.Factor)
				evidence += b.Score
			}
		}
		f := 1.0
		if len(factors) > 0 {
			f = median(factors)
		}
		out = append(out, Entry{Track: r.label, Start: r.start, End: r.end, Factor: f, Evidence: evidence})
	}
	return out
}

// majority is the most common label; ties go to the smallest.
func majority(labels []int) int {
	s := slices.Sorted(slices.Values(labels))
	best, bestN := s[0], 0
	for i := 0; i < len(s); {
		j := i
		for j < len(s) && s[j] == s[i] {
			j++
		}
		if j-i > bestN {
			best, bestN = s[i], j-i
		}
		i = j
	}
	return best
}

func median(v []float64) float64 {
	s := slices.Sorted(slices.Values(v))
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// Align parameters fixed in the reference implementation.
const (
	alignTol        = 0.3 // seconds a match may sit off its track's line
	alignMinWindows = 2
)

// alignSlopes is the reference's slope grid: 0.90 to 1.10 in 0.0025 steps,
// rounded to 4 decimals as numpy does.
func alignSlopes() []float64 {
	var out []float64
	for i := 0; ; i++ {
		v := 0.90 + float64(i)*0.0025
		if v >= 1.1001 {
			return out
		}
		out = append(out, math.RoundToEven(v*1e4)/1e4)
	}
}

type alignPoint struct {
	t, r, w float64 // window start (s), reference time at window start (s), score
	window  int     // window index
}

type trackFit struct {
	track    int
	slope    float64
	evidence map[int]float64 // window index -> on-line score
}

// Align decodes with options ii + iii. rows must be sorted (SortRows).
func Align(rows []Row, window, minEvidence, switchCost float64) []Entry {
	var t0s []float64
	for i, r := range rows {
		if i == 0 || r.T0 != rows[i-1].T0 {
			t0s = append(t0s, r.T0)
		}
	}
	idx := make(map[float64]int, len(t0s))
	for i, t := range t0s {
		idx[t] = i
	}
	// Each track's best row per window (first maximum in row order).
	type key struct {
		t0    float64
		track int
	}
	bestRow := map[key]Row{}
	for _, r := range rows {
		if r.Track < 0 {
			continue
		}
		k := key{r.T0, r.Track}
		if b, ok := bestRow[k]; !ok || r.Score > b.Score {
			bestRow[k] = r
		}
	}
	byTrack := map[int][]alignPoint{}
	for k, r := range bestRow {
		byTrack[k.track] = append(byTrack[k.track],
			alignPoint{t: r.T0, r: r.Offset / FramesPerSecond, w: r.Score, window: idx[r.T0]})
	}
	trackIDs := slices.Sorted(func(yield func(int) bool) {
		for t := range byTrack {
			if !yield(t) {
				return
			}
		}
	})
	slopes := alignSlopes()
	var fits []trackFit
	for _, track := range trackIDs {
		pts := byTrack[track]
		if len(pts) < alignMinWindows {
			continue
		}
		slices.SortFunc(pts, func(a, b alignPoint) int { return cmp.Compare(a.t, b.t) })
		slope, inl := fitLine(pts, alignTol, slopes)
		ev := map[int]float64{}
		total := 0.0
		for i, p := range pts {
			if inl[i] {
				ev[p.window] = p.w
				total += p.w
			}
		}
		if len(ev) < alignMinWindows || total < minEvidence {
			continue
		}
		fits = append(fits, trackFit{track: track, slope: slope, evidence: ev})
	}
	if len(fits) == 0 {
		return nil
	}
	labels := viterbi(fits, len(t0s), minEvidence, switchCost)
	var out []Entry
	for _, r := range runs(labels, t0s, window) {
		for _, f := range fits {
			if f.track != r.label {
				continue
			}
			total := 0.0
			for _, w := range sortedValues(f.evidence) {
				total += w
			}
			out = append(out, Entry{Track: r.label, Start: r.start, End: r.end, Factor: f.slope, Evidence: total})
		}
	}
	return out
}

// sortedValues returns m's values in key order, so sums are reproducible.
func sortedValues(m map[int]float64) []float64 {
	keys := slices.Sorted(func(yield func(int) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	})
	out := make([]float64, len(keys))
	for i, k := range keys {
		out[i] = m[k]
	}
	return out
}

// fitLine finds the set of points with the most weight within tol of some
// line r = a + s*t over the candidate slopes, refits it by weighted least
// squares, and returns the slope and the inliers of the refit line, as the
// reference's fit_line.
func fitLine(pts []alignPoint, tol float64, slopes []float64) (float64, []bool) {
	n := len(pts)
	a := make([]float64, n)
	best, inl := make([]bool, n), make([]bool, n)
	bestWeight := 0.0
	for _, s := range slopes {
		for i, p := range pts {
			a[i] = p.r - float64(s*p.t)
		}
		for _, a0 := range a {
			weight := 0.0
			for i := range pts {
				inl[i] = math.Abs(a[i]-a0) <= tol
				if inl[i] {
					weight += pts[i].w
				}
			}
			if weight > bestWeight {
				bestWeight = weight
				copy(best, inl)
			}
		}
	}
	slope, intercept := 1.0, 0.0
	var sel []alignPoint
	for i, p := range pts {
		if best[i] {
			sel = append(sel, p)
		}
	}
	tMin, tMax := math.Inf(1), math.Inf(-1)
	for _, p := range sel {
		tMin, tMax = math.Min(tMin, p.t), math.Max(tMax, p.t)
	}
	switch {
	case len(sel) >= 2 && tMax > tMin:
		slope, intercept = weightedFit(sel)
	case len(sel) > 0:
		d := make([]float64, len(sel))
		for i, p := range sel {
			d[i] = p.r - p.t
		}
		intercept = median(d)
	}
	out := make([]bool, n)
	for i, p := range pts {
		out[i] = math.Abs(p.r-(intercept+float64(slope*p.t))) <= tol
	}
	return slope, out
}

// weightedFit is least squares r = intercept + slope*t with weights w (what
// numpy.polyfit(t, r, 1, w=sqrt(w)) solves).
func weightedFit(pts []alignPoint) (slope, intercept float64) {
	var sw, st, sr float64
	for _, p := range pts {
		sw += p.w
		st += p.w * p.t
		sr += p.w * p.r
	}
	tm, rm := st/sw, sr/sw
	var stt, str float64
	for _, p := range pts {
		dt := p.t - tm
		stt += p.w * dt * dt
		str += p.w * dt * (p.r - rm)
	}
	slope = str / stt
	return slope, rm - slope*tm
}

// viterbi picks one state per window: index 0 is unknown, j >= 1 is
// fits[j-1]. Staying is free, switching costs switchCost; a window earns
// log1p(score) for a track it has on-line evidence for, and unknown earns
// log1p(minEvidence / alignMinWindows).
func viterbi(fits []trackFit, n int, minEvidence, switchCost float64) []int {
	m := len(fits) + 1
	emit := make([][]float64, n)
	for i := range emit {
		emit[i] = make([]float64, m)
		emit[i][0] = math.Log1p(minEvidence / alignMinWindows)
	}
	for j, f := range fits {
		for i, sc := range f.evidence {
			emit[i][j+1] = math.Log1p(sc)
		}
	}
	score := make([][]float64, n)
	back := make([][]int, n)
	score[0] = slices.Clone(emit[0])
	back[0] = make([]int, m)
	for i := 1; i < n; i++ {
		prev := score[i-1]
		k := argmax(prev)
		move := prev[k] - switchCost
		score[i], back[i] = make([]float64, m), make([]int, m)
		for j := range m {
			if move > prev[j] {
				score[i][j], back[i][j] = emit[i][j]+move, k
			} else {
				score[i][j], back[i][j] = emit[i][j]+prev[j], j
			}
		}
	}
	path := make([]int, n)
	path[n-1] = argmax(score[n-1])
	for i := n - 1; i > 0; i-- {
		path[i-1] = back[i][path[i]]
	}
	labels := make([]int, n)
	for i, j := range path {
		labels[i] = -1
		if j > 0 {
			labels[i] = fits[j-1].track
		}
	}
	return labels
}

// argmax returns the first index of the largest value.
func argmax(v []float64) int {
	best := 0
	for i, x := range v {
		if x > v[best] {
			best = i
		}
	}
	return best
}

// MergeRepeats joins consecutive entries of the same track that overlap or
// sit within gap seconds of each other (a decoder that drops to "unknown"
// for a window and comes back splits one play into several). A merged entry
// keeps the larger evidence and that entry's tempo factor. entries must be
// in start order, as the decoders return them.
func MergeRepeats(entries []Entry, gap float64) []Entry {
	var out []Entry
	for _, e := range entries {
		if n := len(out); n > 0 && out[n-1].Track == e.Track && e.Start <= out[n-1].End+gap {
			last := &out[n-1]
			last.End = math.Max(last.End, e.End)
			if e.Evidence > last.Evidence {
				last.Evidence, last.Factor = e.Evidence, e.Factor
			}
			continue
		}
		out = append(out, e)
	}
	return out
}
