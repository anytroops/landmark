package match

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// realisticVotes mimics Matcher votes: few tracks, offsets near the bias.
func realisticVotes(rng *rand.Rand, n int, tracks uint32, offRange int) []uint64 {
	v := make([]uint64, n)
	for i := range v {
		off := int64(rng.IntN(2*offRange)-offRange) + offsetBias
		v[i] = uint64(rng.Uint32N(tracks))<<32 | uint64(uint32(off))
	}
	return v
}

func TestSortVotesMatchesSlicesSort(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	tests := []struct {
		name string
		in   []uint64
	}{
		{"empty", nil},
		{"tiny", []uint64{5, 1, 4}},
		{"below threshold", realisticVotes(rng, radixMin-1, 50, 2000)},
		{"realistic", realisticVotes(rng, 100_000, 2000, 5000)},
		{"one track", realisticVotes(rng, 10_000, 1, 300)},
		{"all equal", slices.Repeat([]uint64{42}, 5000)},
		{"full 64-bit range", func() []uint64 {
			v := make([]uint64, 5000)
			for i := range v {
				v[i] = rng.Uint64()
			}
			return v
		}()},
		{"offsets crossing the bias", realisticVotes(rng, 20_000, 10, 1<<20)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := slices.Clone(tc.in)
			slices.Sort(want)
			got := sortVotes(slices.Clone(tc.in), make([]uint64, len(tc.in)))
			if !slices.Equal(got, want) {
				t.Fatal("radix sort disagrees with slices.Sort")
			}
		})
	}
}

func BenchmarkSortVotes(b *testing.B) {
	rng := rand.New(rand.NewPCG(1, 2))
	base := realisticVotes(rng, 100_000, 2000, 5000)
	work, buf := make([]uint64, len(base)), make([]uint64, len(base))
	b.Run("pdqsort", func(b *testing.B) {
		for b.Loop() {
			copy(work, base)
			slices.Sort(work)
		}
	})
	b.Run("radix", func(b *testing.B) {
		for b.Loop() {
			copy(work, base)
			sortVotes(work, buf)
		}
	})
}
