package triplet

import (
	"context"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/anytroops/landmark/internal/audio"
)

// separateQuery is FingerprintQuery as it was before D44: every filterbank
// fingerprints every shift with its own FFTs.
func separateQuery(pcm []float32, nShifts int) []Landmark {
	p := DefaultParams()
	step := p.Hop / nShifts
	var all []Landmark
	for _, f := range []*Fingerprinter{New(p), New(HalfBand(p))} {
		for k := 0; k < nShifts && k*step < len(pcm); k++ {
			all = append(all, f.Fingerprint(pcm[k*step:])...)
		}
	}
	slices.SortFunc(all, func(a, b Landmark) int {
		if a.Hash != b.Hash {
			return int(a.Hash) - int(b.Hash)
		}
		if a.T != b.T {
			return int(a.T) - int(b.T)
		}
		if a.Span != b.Span {
			return int(a.Span) - int(b.Span)
		}
		return int(a.Band) - int(b.Band)
	})
	return slices.Compact(all)
}

// TestFingerprintQuerySharesFFTsExactly checks that computing each frame's
// FFT once for both filterbanks (D44) changes no landmark.
func TestFingerprintQuerySharesFFTsExactly(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	noise := make([]float32, 5*audio.SampleRate)
	for i := range noise {
		noise[i] = float32(rng.NormFloat64() * 0.1)
	}
	synth := syntheticSignal(10 * audio.SampleRate)
	type queryCase struct {
		name   string
		pcm    []float32
		shifts int
	}
	tests := []queryCase{
		{"synthetic window", synth, 4},
		{"double time", audio.ResamplePoly(synth, 2, 1), 4},
		{"half time", audio.ResamplePoly(synth, 1, 2), 4},
		{"noise, one shift", noise, 1},
		{"shorter than a frame for later shifts", synth[:4096+100], 4},
		{"shorter than a frame", synth[:1000], 4},
	}
	// A real track when the dataset is present (fma_medium 001966, one of
	// the golden tracks), 10 s from 5 s in.
	if mp3 := filepath.Join(repoRoot, "data", "fma_medium", "001", "001966.mp3"); fileExists(mp3) {
		pcm, err := audio.Decode(context.Background(), mp3, audio.SampleRate)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		x := pcm[5*audio.SampleRate : 15*audio.SampleRate]
		tests = append(tests, queryCase{"fma 001966", x, 4},
			queryCase{"fma 001966 double time", audio.ResamplePoly(x, 2, 1), 4})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New(DefaultParams()).FingerprintQuery(tt.pcm, tt.shifts)
			want := separateQuery(tt.pcm, tt.shifts)
			if !slices.Equal(got, want) {
				t.Fatalf("got %d landmarks, separate filterbanks give %d", len(got), len(want))
			}
			if len(tt.pcm) > 10000 && len(got) == 0 {
				t.Fatal("no landmarks: the test compares nothing")
			}
		})
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestKeptBuffersDoNotLeak: a Fingerprinter and a Stream keep their
// buffers from call to call (D51). Landmarks they returned must not change
// with later calls, and a reused Fingerprinter must give what a new one
// gives, for longer and shorter signals in turn.
func TestKeptBuffersDoNotLeak(t *testing.T) {
	x := syntheticSignal(40 * audio.SampleRate)
	sec := func(a, n int) []float32 { return x[a*audio.SampleRate : (a+n)*audio.SampleRate] }
	spans := [][2]int{{0, 10}, {5, 3}, {12, 20}, {1, 0}, {30, 10}}
	fp := New(DefaultParams())
	var query, index [][]Landmark
	for _, s := range spans {
		query = append(query, fp.FingerprintQuery(sec(s[0], s[1]), 4))
		index = append(index, fp.Fingerprint(sec(s[0], s[1])))
	}
	for i, s := range spans {
		if want := New(DefaultParams()).FingerprintQuery(sec(s[0], s[1]), 4); !slices.Equal(query[i], want) {
			t.Errorf("FingerprintQuery %v: %d landmarks, a new Fingerprinter %d", s, len(query[i]), len(want))
		}
		if want := New(DefaultParams()).Fingerprint(sec(s[0], s[1])); !slices.Equal(index[i], want) {
			t.Errorf("Fingerprint %v: %d landmarks, a new Fingerprinter %d", s, len(index[i]), len(want))
		}
	}

	st, err := NewStream(DefaultParams(), 4)
	if err != nil {
		t.Fatal(err)
	}
	st.Push(x)
	const hop, n = 27520, 10 * audio.SampleRate
	var got [][]Landmark
	for start := 0; start+n <= len(x) && len(got) < 8; start += hop {
		lms, err := st.Query(start, n)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, lms)
	}
	for i, lms := range got {
		if want := New(DefaultParams()).FingerprintQuery(x[i*hop:i*hop+n], 4); !slices.Equal(lms, want) {
			t.Errorf("Stream window %d, checked after later windows: %d landmarks, want %d", i, len(lms), len(want))
		}
	}
}
