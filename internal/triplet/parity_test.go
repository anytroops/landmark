package triplet

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/anytroops/landmark/internal/audio"
)

// minParityJaccard is the bar for Go vs Python landmark sets (D17): both sides
// use float64, so only FFT rounding can differ.
const minParityJaccard = 0.999

const repoRoot = "../.."

type landmarkSet struct {
	Hashes []uint32 `json:"hashes"`
	Times  []uint32 `json:"times"`
	Spans  []uint8  `json:"spans"`
	Bands  []uint8  `json:"bands"`
}

func (s landmarkSet) landmarks(t *testing.T) []Landmark {
	t.Helper()
	if len(s.Hashes) != len(s.Times) || len(s.Hashes) != len(s.Spans) || len(s.Hashes) != len(s.Bands) {
		t.Fatalf("golden arrays differ in length")
	}
	out := make([]Landmark, len(s.Hashes))
	for i := range out {
		out[i] = Landmark{Hash: s.Hashes[i], T: s.Times[i], Span: s.Spans[i], Band: s.Bands[i]}
	}
	return out
}

type golden struct {
	landmarkSet
	Signal   string      `json:"signal"`
	FMAID    int         `json:"fma_id"`
	Subset   string      `json:"subset"`
	Params   Params      `json:"params"`
	NSamples int         `json:"n_samples"`
	Query    landmarkSet `json:"query"`
}

func loadGolden(t *testing.T, path string) golden {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var g golden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if g.Params != DefaultParams() {
		t.Fatalf("%s: golden params %+v differ from DefaultParams %+v; regenerate with python -m prototype.golden_b2",
			path, g.Params, DefaultParams())
	}
	return g
}

// syntheticSignal must match prototype.golden.synthetic_signal exactly (as
// internal/fingerprint's copy does).
func syntheticSignal(n int) []float32 {
	const sr = 11025.0
	out := make([]float32, n)
	state := uint32(12345)
	for i := range out {
		tau := float64(i) / sr
		chirp := 0.25 * math.Sin(2*math.Pi*(150*tau+0.5*250*tau*tau))
		gate := 0.0
		if int64(math.Floor(tau*4))%3 == 0 {
			gate = 1
		}
		tones := 0.15*gate*math.Sin(2*math.Pi*440*tau) + 0.1*math.Sin(2*math.Pi*1234.5*tau)
		state = state*1664525 + 1013904223
		noise := 0.02 * (float64(state)/(1<<32) - 0.5)
		out[i] = float32(chirp + tones + noise)
	}
	return out
}

func jaccard(a, b []Landmark) float64 {
	in := map[Landmark]int{}
	for _, l := range a {
		in[l] |= 1
	}
	for _, l := range b {
		in[l] |= 2
	}
	both := 0
	for _, v := range in {
		if v == 3 {
			both++
		}
	}
	if len(in) == 0 {
		return 1
	}
	return float64(both) / float64(len(in))
}

func TestParitySynthetic(t *testing.T) {
	g := loadGolden(t, filepath.Join(repoRoot, "testdata", "golden_b2", "synthetic.json"))
	fp := New(DefaultParams())
	x := syntheticSignal(g.NSamples)
	if got, want := fp.Fingerprint(x), g.landmarks(t); jaccard(got, want) < minParityJaccard {
		t.Errorf("full clip: Jaccard = %.5f (%d Go vs %d Python)", jaccard(got, want), len(got), len(want))
	}
	got, want := fp.FingerprintQuery(x[:10*11025], 4), g.Query.landmarks(t)
	if j := jaccard(got, want); j < minParityJaccard {
		t.Errorf("query: Jaccard = %.5f (%d Go vs %d Python)", j, len(got), len(want))
	}
	if len(got) == len(want) {
		for i := range got { // same set should come out in the same order
			if got[i] != want[i] {
				t.Errorf("query landmark %d = %+v, Python %+v", i, got[i], want[i])
				break
			}
		}
	}
}

// TestParityFMA compares against real tracks; it skips without data/.
func TestParityFMA(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot, "testdata", "golden_b2", "[0-9]*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no FMA golden files: %v", err)
	}
	fp := New(DefaultParams())
	for _, path := range files {
		g := loadGolden(t, path)
		t.Run(fmt.Sprintf("fma_%06d", g.FMAID), func(t *testing.T) {
			id := fmt.Sprintf("%06d", g.FMAID)
			mp3 := filepath.Join(repoRoot, "data", "fma_"+g.Subset, id[:3], id+".mp3")
			if _, err := os.Stat(mp3); err != nil {
				t.Skip("dataset not present")
			}
			pcm, err := audio.Decode(context.Background(), mp3, audio.SampleRate)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(pcm) != g.NSamples {
				t.Fatalf("decoded %d samples, Python had %d", len(pcm), g.NSamples)
			}
			got, want := fp.Fingerprint(pcm), g.landmarks(t)
			j := jaccard(got, want)
			t.Logf("Jaccard %.6f (%d Go, %d Python)", j, len(got), len(want))
			if j < minParityJaccard {
				t.Errorf("Jaccard = %.5f, want >= %v", j, minParityJaccard)
			}
		})
	}
}

func TestFilterbankRowsSumToOne(t *testing.T) {
	p := DefaultParams()
	for b, row := range Filterbank(p) {
		s := 0.0
		for _, v := range row {
			s += v
		}
		if math.Abs(s-1) > 1e-12 {
			t.Fatalf("band %d sums to %v", b, s)
		}
	}
}
