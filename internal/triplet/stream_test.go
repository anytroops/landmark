package triplet

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/anytroops/landmark/internal/audio"
)

// TestStreamQueryEqualsFingerprintQuery feeds a signal in uneven pieces and
// checks every window, on the shared front end's hop (D48), against
// FingerprintQuery of the same samples, landmark for landmark.
func TestStreamQueryEqualsFingerprintQuery(t *testing.T) {
	x := syntheticSignal(40 * audio.SampleRate)
	tests := []struct {
		name      string
		hop, size int
	}{
		{"10 s windows every 27,520 samples", 27520, 10 * audio.SampleRate},
		{"double-time windows", 2 * 27520, 2 * 10 * audio.SampleRate},
		{"half-time windows", 27520 / 2, 10 * audio.SampleRate / 2},
		{"short windows, later shifts without frames", 128, 4096 + 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, err := NewStream(DefaultParams(), 4)
			if err != nil {
				t.Fatal(err)
			}
			fp := New(DefaultParams())
			rng := rand.New(rand.NewPCG(7, uint64(tt.hop)))
			next, pushed, checked := 0, 0, 0
			for pushed < len(x) {
				n := min(1+rng.IntN(9000), len(x)-pushed)
				st.Push(x[pushed : pushed+n])
				pushed += n
				for next+tt.size <= pushed && st.Ready(next, tt.size) && checked < 12 {
					got, err := st.Query(next, tt.size)
					if err != nil {
						t.Fatal(err)
					}
					want := fp.FingerprintQuery(x[next:next+tt.size], 4)
					if !slices.Equal(got, want) {
						t.Fatalf("window at %d: %d landmarks, FingerprintQuery %d", next, len(got), len(want))
					}
					next += tt.hop
					st.Forget(next)
					checked++
				}
			}
			if checked < 5 {
				t.Fatalf("only %d windows checked", checked)
			}
		})
	}
}

func TestStreamRefuses(t *testing.T) {
	st, err := NewStream(DefaultParams(), 4)
	if err != nil {
		t.Fatal(err)
	}
	x := syntheticSignal(12 * audio.SampleRate)
	st.Push(x[:5*audio.SampleRate])
	if _, err := st.Query(64*3+1, 4096); err == nil {
		t.Error("off-grid start accepted")
	}
	if _, err := st.Query(0, 10*audio.SampleRate); err == nil {
		t.Error("window past the pushed samples accepted")
	}
	st.Push(x[5*audio.SampleRate:])
	st.Forget(2 * audio.SampleRate)
	if _, err := st.Query(0, 4096); err == nil {
		t.Error("forgotten window accepted")
	}
	if _, err := st.Query(2*audio.SampleRate/64*64+64, 4096); err != nil {
		t.Errorf("window after the forgotten part: %v", err)
	}
	if _, err := NewStream(DefaultParams(), 3); err == nil {
		t.Error("256-sample hop split into 3 accepted")
	}
}
