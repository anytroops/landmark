package fingerprint

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

// minParityJaccard is the bar for Go vs Python landmark sets (D17). Both
// sides use float64, so only FFT rounding can differ; anything lower is a bug.
const minParityJaccard = 0.999

const repoRoot = "../.."

type golden struct {
	Signal   string   `json:"signal"`
	FMAID    int      `json:"fma_id"`
	Subset   string   `json:"subset"`
	Params   Params   `json:"params"`
	NSamples int      `json:"n_samples"`
	Hashes   []uint32 `json:"hashes"`
	Times    []uint32 `json:"times"`
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
		t.Fatalf("%s: golden params %+v differ from DefaultParams %+v; regenerate with python -m prototype.golden",
			path, g.Params, DefaultParams())
	}
	if len(g.Hashes) != len(g.Times) {
		t.Fatalf("%s: %d hashes vs %d times", path, len(g.Hashes), len(g.Times))
	}
	return g
}

func (g golden) landmarks() []Landmark {
	out := make([]Landmark, len(g.Hashes))
	for i := range g.Hashes {
		out[i] = Landmark{Hash: g.Hashes[i], T: g.Times[i]}
	}
	return out
}

// syntheticSignal must match prototype.golden.synthetic_signal exactly.
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
		state = state*1664525 + 1013904223 // wraps mod 2^32, like the Python mask
		noise := 0.02 * (float64(state)/(1<<32) - 0.5)
		out[i] = float32(chirp + tones + noise)
	}
	return out
}

func TestParitySynthetic(t *testing.T) {
	g := loadGolden(t, filepath.Join(repoRoot, "testdata", "golden", "synthetic.json"))
	got := New(DefaultParams()).Fingerprint(syntheticSignal(g.NSamples))
	if j := jaccard(got, g.landmarks()); j < minParityJaccard {
		t.Errorf("Jaccard = %.5f (%d Go vs %d Python landmarks), want >= %v",
			j, len(got), len(g.Hashes), minParityJaccard)
	}
}

// TestParityFMA compares against real tracks. It needs the local dataset in
// data/ and skips without it (CI has no audio).
func TestParityFMA(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot, "testdata", "golden", "[0-9]*.json"))
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
			got := fp.Fingerprint(pcm)
			j := jaccard(got, g.landmarks())
			t.Logf("Jaccard %.6f, %d landmarks differ (%d Go, %d Python)",
				j, symmetricDiff(got, g.landmarks()), len(got), len(g.Hashes))
			if j < minParityJaccard {
				t.Errorf("Jaccard = %.5f, want >= %v", j, minParityJaccard)
			}
		})
	}
}

// symmetricDiff counts landmarks present in exactly one of a and b.
func symmetricDiff(a, b []Landmark) int {
	in := map[Landmark]int{}
	for _, l := range a {
		in[l] |= 1
	}
	for _, l := range b {
		in[l] |= 2
	}
	n := 0
	for _, v := range in {
		if v != 3 {
			n++
		}
	}
	return n
}
