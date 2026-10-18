package fingerprint

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestHashPackUnpack(t *testing.T) {
	tests := []struct{ f1, f2, dt int }{
		{0, 0, 1}, {511, 511, 63}, {100, 164, 7}, {300, 236, 63},
	}
	for _, tc := range tests {
		peaks := []Peak{{T: 0, F: tc.f1}, {T: tc.dt, F: tc.f2}}
		p := DefaultParams()
		p.MaxDF = NBins // allow any df for this test
		lm := Pair(peaks, p)
		if len(lm) != 1 {
			t.Fatalf("%+v: got %d landmarks, want 1", tc, len(lm))
		}
		f1, f2, dt := Unpack(lm[0].Hash)
		if f1 != tc.f1 || f2 != tc.f2 || dt != tc.dt {
			t.Errorf("Unpack = (%d, %d, %d), want %+v", f1, f2, dt, tc)
		}
	}
}

func TestSpectrogramShapeAndToneBin(t *testing.T) {
	const sr, hz = 11025, 1000.0
	pcm := make([]float32, sr)
	for i := range pcm {
		pcm[i] = float32(math.Sin(2 * math.Pi * hz * float64(i) / sr))
	}
	s := New(DefaultParams()).Spectrogram(pcm)
	if want := 1 + (sr-1024)/256; s.Frames != want {
		t.Fatalf("frames = %d, want %d", s.Frames, want)
	}
	best, bestF := math.Inf(-1), -1
	for f := 0; f < NBins; f++ {
		if v := s.At(s.Frames/2, f); v > best {
			best, bestF = v, f
		}
	}
	if want := int(math.Round(hz * 1024 / sr)); abs(bestF-want) > 1 {
		t.Errorf("peak bin = %d, want %d", bestF, want)
	}
}

func TestShortAndSilentInput(t *testing.T) {
	fp := New(DefaultParams())
	tests := []struct {
		name string
		n    int
	}{
		{"shorter than one frame", 500},
		{"five seconds of silence", 5 * 11025},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := fp.Fingerprint(make([]float32, tc.n)); len(got) != 0 {
				t.Errorf("got %d landmarks, want 0", len(got))
			}
		})
	}
}

// naiveMaxFilter is the direct 2D definition, used to check the separable one.
func naiveMaxFilter(s Spectrogram, sizeT, sizeF int) []float64 {
	out := make([]float64, len(s.Data))
	for t := 0; t < s.Frames; t++ {
		for f := 0; f < NBins; f++ {
			m := math.Inf(-1)
			for dt := -sizeT / 2; dt <= sizeT/2; dt++ {
				for df := -sizeF / 2; df <= sizeF/2; df++ {
					tt, ff := t+dt, f+df
					if tt >= 0 && tt < s.Frames && ff >= 0 && ff < NBins {
						m = math.Max(m, s.At(tt, ff))
					}
				}
			}
			out[t*NBins+f] = m
		}
	}
	return out
}

func TestMaxFilterMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, frames := range []int{1, 5, 40} {
		s := Spectrogram{Frames: frames, Bins: NBins, Data: make([]float64, frames*NBins)}
		for i := range s.Data {
			s.Data[i] = rng.Float64()
		}
		got, want := maxFilter(s, 17, 17), naiveMaxFilter(s, 17, 17)
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("frames=%d: cell %d = %v, want %v", frames, i, got[i], want[i])
			}
		}
	}
}

func TestPeaksCappedPerBlockAndSorted(t *testing.T) {
	p := DefaultParams()
	peaks := FindPeaks(New(p).Spectrogram(syntheticSignal(10*11025)), p)
	if len(peaks) == 0 {
		t.Fatal("no peaks")
	}
	count := map[int]int{}
	for i, pk := range peaks {
		count[pk.T/p.BlockFrames]++
		if i > 0 {
			prev := peaks[i-1]
			if pk.T < prev.T || (pk.T == prev.T && pk.F <= prev.F) {
				t.Fatalf("peaks not sorted by (t, f) at %d", i)
			}
		}
	}
	for b, n := range count {
		if n > p.PeaksPerBlock {
			t.Errorf("block %d has %d peaks, cap %d", b, n, p.PeaksPerBlock)
		}
	}
}

func TestLandmarksRespectZoneAndFanout(t *testing.T) {
	p := DefaultParams()
	lms := New(p).Fingerprint(syntheticSignal(10 * 11025))
	if len(lms) == 0 {
		t.Fatal("no landmarks")
	}
	perAnchor := map[[2]int]int{}
	for _, lm := range lms {
		f1, f2, dt := Unpack(lm.Hash)
		if dt < p.MinDT || dt > p.MaxDT || abs(f2-f1) > p.MaxDF {
			t.Fatalf("landmark outside target zone: f1=%d f2=%d dt=%d", f1, f2, dt)
		}
		perAnchor[[2]int{int(lm.T), f1}]++
	}
	for k, n := range perAnchor {
		if n > p.Fanout {
			t.Errorf("anchor %v has %d pairs, fanout %d", k, n, p.Fanout)
		}
	}
}

func TestFingerprintQuery(t *testing.T) {
	fp := New(DefaultParams())
	pcm := syntheticSignal(5 * 11025)

	t.Run("one shift is the plain set", func(t *testing.T) {
		if j := jaccard(fp.Fingerprint(pcm), fp.FingerprintQuery(pcm, 1)); j != 1 {
			t.Errorf("jaccard = %v, want 1", j)
		}
	})
	t.Run("sorted and unique", func(t *testing.T) {
		q := fp.FingerprintQuery(pcm, 4)
		for i := 1; i < len(q); i++ {
			a, b := q[i-1], q[i]
			if a.Hash > b.Hash || (a.Hash == b.Hash && a.T >= b.T) {
				t.Fatalf("not strictly sorted at %d: %+v then %+v", i, a, b)
			}
		}
	})
}

func jaccard(a, b []Landmark) float64 {
	sa := make(map[Landmark]struct{}, len(a))
	for _, l := range a {
		sa[l] = struct{}{}
	}
	sb := make(map[Landmark]struct{}, len(b))
	inter := 0
	for _, l := range b {
		if _, dup := sb[l]; dup {
			continue
		}
		sb[l] = struct{}{}
		if _, ok := sa[l]; ok {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}
