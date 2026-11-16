package analysis

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/anytroops/landmark/internal/fingerprint"
)

func noiseSpec(frames int, seed uint64) fingerprint.Spectrogram {
	rng := rand.New(rand.NewPCG(seed, 0))
	s := fingerprint.Spectrogram{Frames: frames, Bins: 234, Data: make([]float64, frames*234)}
	for i := range s.Data {
		s.Data[i] = 6 * rng.NormFloat64()
	}
	return s
}

func TestWindowCorrIsOneAtTheRightAlignmentAndNearZeroOff(t *testing.T) {
	ref := noiseSpec(4000, 1)
	const skip = 861 // the mix starts this many frames into ref (about 20 s)
	a := float64(skip) / FramesPerSecond
	mix := fingerprint.Spectrogram{Frames: ref.Frames - skip, Bins: ref.Bins, Data: ref.Data[skip*ref.Bins:]}
	n := frames(checkWindow)
	if c := windowCorr(mix, ref, 100, n, a, 1, 0); math.Abs(c-1) > 0.01 {
		t.Errorf("right alignment: corr %.3f, want 1", c)
	}
	if c := windowCorr(mix, ref, 100, n, a+7.3, 1, 0); math.Abs(c) > 0.1 {
		t.Errorf("7.3 s off: corr %.3f, want about 0", c)
	}
	if c := windowCorr(mix, noiseSpec(4000, 2), 100, n, a, 1, 0); math.Abs(c) > 0.1 {
		t.Errorf("another song: corr %.3f, want about 0", c)
	}
	if c := windowCorr(mix, ref, 3000, n, 200, 1, 0); !math.IsNaN(c) {
		t.Errorf("a window outside the reference: corr %.3f, want NaN", c)
	}
}

func TestScoreAlignmentFindsATurntablePitchShift(t *testing.T) {
	ref := noiseSpec(4000, 3)
	const shift = 3
	rate := math.Pow(2, shift/36.0)
	// The mix: ref's bands moved up by shift, then played rate times faster
	// (frame t of the mix shows ref frame t*rate).
	mix := fingerprint.Spectrogram{Frames: 2000, Bins: ref.Bins, Data: make([]float64, 2000*ref.Bins)}
	for tm := 0; tm < mix.Frames; tm++ {
		pos := float64(tm) * rate
		i0 := int(pos)
		w := pos - float64(i0)
		for b := margin + shift; b < ref.Bins-margin+shift; b++ {
			mix.Data[tm*mix.Bins+b] = (1-w)*ref.At(i0, b-shift) + w*ref.At(i0+1, b-shift)
		}
	}
	corr, pitch := scoreAlignment(mix, ref, []int{50}, 0, rate)
	if !pitch || corr < 0.5 {
		t.Errorf("corr %.3f pitch %v, want a pitched fit above 0.5", corr, pitch)
	}
}

func TestCheckStartsCoverTheEntry(t *testing.T) {
	total := frames(60)
	if w := checkStarts(0, 3, total); len(w) != 1 {
		t.Errorf("a short entry: %d windows, want 1", len(w))
	}
	w := checkStarts(5, 40, total)
	n := frames(checkWindow)
	if w[0] != frames(5) || len(w) < 5 || w[len(w)-1]+n > frames(40)+1 {
		t.Errorf("windows %v do not cover 5 to 40 s", w)
	}
}

func TestVerifyPassesTheRightSongAndFailsAnother(t *testing.T) {
	ref := noiseSpec(6000, 4)
	const skip = 1292 // the mix starts this many frames into ref (about 30 s)
	a := float64(skip) / FramesPerSecond
	mix := fingerprint.Spectrogram{Frames: ref.Frames - skip, Bins: ref.Bins, Data: ref.Data[skip*ref.Bins:]}
	pool := []fingerprint.Spectrogram{ref}
	for i := range 8 {
		pool = append(pool, noiseSpec(5000, uint64(10+i)))
	}
	rng := rand.New(rand.NewPCG(7, 0))
	right := Verify(mix, ref, pool, a, 1, 0, 60, rng)
	if !right.Passed() || right.Corr < 0.99 {
		t.Errorf("the right song: %+v, want a pass with corr about 1", right)
	}
	wrong := Verify(mix, pool[1], pool, a, 1, 0, 60, rng)
	if wrong.Passed() || math.Abs(wrong.Z) > 3 {
		t.Errorf("another song: %+v, want a fail near z 0", wrong)
	}
	if out := Verify(mix, ref, pool, a, 1, 500, 520, rng); !math.IsNaN(out.Corr) || out.Passed() {
		t.Errorf("an entry past the end of the mix: %+v, want NaN and no pass", out)
	}
}
