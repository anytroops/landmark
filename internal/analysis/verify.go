package analysis

import (
	"math"
	"math/rand/v2"
	"slices"

	"github.com/anytroops/landmark/internal/fingerprint"
)

// The audio check of a tracklist entry (D57; a port of eval/verify_set.py):
// whether the song the entry names is in the mix where the entry says,
// judged on the audio and not on the fingerprints' votes. The song's log
// spectrogram is warped onto the mix by the entry's alignment (with the
// pitch moved, or not, whichever fits: turntable or key lock), and each
// checkWindow of the entry is compared with the mix band by band: each
// band's level over time, less its mean, correlated with the mix's in the
// same band, averaged over the bands. Removing the means makes EQ moves and
// the other song's steady energy matter little. The entry's score is the
// median over its windows. The null is the same statistic with other songs
// of the catalog at random positions, the same windows; Z is the score in
// standard deviations of that null.
const (
	checkWindow = 10.0 // seconds
	checkHop    = 5.0
	checkNulls  = 40
	// checkCoverage is the least of a window that must fall inside the
	// reference for the window to count.
	checkCoverage = 0.8
	// VerifyMark is the Z an entry must reach: set on UnmixDB's calibration
	// sets so that 1% of wrong candidates pass (eval.verify_calibrate).
	VerifyMark = 4.23
)

// Check is one entry's audio check. Corr is NaN and Z zero when no window
// of the entry falls inside the reference.
type Check struct {
	Corr     float64
	Pitch    bool // the better fit moved the pitch (a turntable change)
	NullMean float64
	NullStd  float64
	Z        float64
}

// Passed is whether the check reached VerifyMark.
func (c Check) Passed() bool { return !math.IsNaN(c.Corr) && c.Z >= VerifyMark }

// frames truncates seconds to frames, as int(s * FPS) does in the prototype.
func frames(s float64) int { return int(s * FramesPerSecond) }

// windowCorr is the mean per-band correlation of mix frames f0 .. f0+n
// with ref warped by (a, rate), ref's bands moved up by shift; NaN when
// under checkCoverage of the window falls inside ref.
func windowCorr(mix, ref fingerprint.Spectrogram, f0, n int, a, rate float64, shift int) float64 {
	last := min(f0+n, mix.Frames)
	var frames, idx []int
	for t := f0; t < last; t++ {
		i := int(math.RoundToEven((a + rate*float64(t)/FramesPerSecond) * FramesPerSecond))
		if i >= 0 && i < ref.Frames {
			frames, idx = append(frames, t), append(idx, i)
		}
	}
	if float64(len(frames)) < checkCoverage*float64(n) {
		return math.NaN()
	}
	bands := ref.Bins
	sum, count := 0.0, 0
	for b := margin; b < bands-margin; b++ {
		rb := b - shift
		mm, rm := 0.0, 0.0
		for k, t := range frames {
			mm += mix.At(t, b)
			rm += ref.At(idx[k], rb)
		}
		mm /= float64(len(frames))
		rm /= float64(len(frames))
		var mr, m2, r2 float64
		for k, t := range frames {
			dm, dr := mix.At(t, b)-mm, ref.At(idx[k], rb)-rm
			mr, m2, r2 = mr+dm*dr, m2+dm*dm, r2+dr*dr
		}
		if denom := math.Sqrt(m2 * r2); denom > 1e-6 {
			sum += mr / denom
			count++
		}
	}
	if count == 0 {
		return math.NaN()
	}
	return sum / float64(count)
}

// checkStarts returns window starts (frames) across [start, end] seconds:
// at least one, every checkHop.
func checkStarts(start, end float64, total int) []int {
	n := frames(checkWindow)
	first := max(frames(start), 0)
	last := max(first, min(frames(end), total)-n)
	step := max(frames(checkHop), 1)
	var out []int
	for f := first; f <= last; f += step {
		out = append(out, f)
	}
	return out
}

// scoreAlignment is the median window correlation at the better of the two
// pitch treatments, and whether that was the pitched one; NaN if no window
// falls inside ref.
func scoreAlignment(mix, ref fingerprint.Spectrogram, windows []int, a, rate float64) (float64, bool) {
	n := frames(checkWindow)
	best, bestPitch, found := 0.0, false, false
	for _, pitch := range []bool{false, true} {
		shift := 0
		if pitch {
			shift = int(math.RoundToEven(bandsPerOctave * math.Log2(rate)))
			if shift == 0 || shift > margin || shift < -margin {
				continue
			}
		}
		var c []float64
		for _, w := range windows {
			if x := windowCorr(mix, ref, w, n, a, rate, shift); !math.IsNaN(x) {
				c = append(c, x)
			}
		}
		if len(c) == 0 {
			continue
		}
		if m := median(c); !found || m > best {
			best, bestPitch, found = m, pitch, true
		}
	}
	if !found {
		return math.NaN(), false
	}
	return best, bestPitch
}

// Verify checks one entry: the song's spectrogram ref at alignment (a,
// rate) over [start, end] seconds of mix, against nulls drawn from pool
// (other songs' spectrograms; one is skipped if it is ref itself) with rng.
func Verify(mix, ref fingerprint.Spectrogram, pool []fingerprint.Spectrogram, a, rate, start, end float64, rng *rand.Rand) Check {
	windows := checkStarts(start, end, mix.Frames)
	corr, pitch := scoreAlignment(mix, ref, windows, a, rate)
	c := Check{Corr: corr, Pitch: pitch}
	if math.IsNaN(corr) || len(pool) == 0 {
		return c
	}
	others := slices.DeleteFunc(slices.Clone(pool), func(s fingerprint.Spectrogram) bool {
		return len(s.Data) == len(ref.Data) && len(s.Data) > 0 && &s.Data[0] == &ref.Data[0]
	})
	if len(others) == 0 {
		return c
	}
	var null []float64
	for tries := 0; len(null) < checkNulls && tries < 20*checkNulls; tries++ {
		other := others[rng.IntN(len(others))]
		span := float64(other.Frames) / FramesPerSecond
		fakeA := rng.Float64()*math.Max(span-checkWindow, 1) - start
		if x, _ := scoreAlignment(mix, other, windows, fakeA, 1.0); !math.IsNaN(x) {
			null = append(null, x)
		}
	}
	if len(null) < 2 {
		return c
	}
	mean := 0.0
	for _, x := range null {
		mean += x
	}
	mean /= float64(len(null))
	v := 0.0
	for _, x := range null {
		v += (x - mean) * (x - mean)
	}
	std := math.Sqrt(v / float64(len(null)))
	c.NullMean, c.NullStd = mean, std
	if std > 0 {
		c.Z = (corr - mean) / std
	}
	return c
}
