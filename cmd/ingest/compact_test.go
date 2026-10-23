package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	versioned "github.com/anytroops/landmark/internal/manifest"
	"github.com/anytroops/landmark/internal/segment"
)

func TestTierPlan(t *testing.T) {
	pol := tierPolicy{FanIn: 4, BaseFraction: 0.1, MaxDeltas: 6}
	tests := []struct {
		name  string
		sizes []uint64 // base first
		want  []group
	}{
		{"base alone", []uint64{1000}, nil},
		{"three of a tier wait", []uint64{100000, 20, 20, 20}, nil},
		{"four of a tier merge", []uint64{100000, 20, 20, 20, 20}, []group{{1, 4}}},
		{"only the newest four", []uint64{100000, 500, 20, 20, 20, 20}, []group{{2, 5}}},
		{"cascade", []uint64{100000, 80, 80, 80, 20, 20, 20, 20}, []group{{1, 7}}},
		{"tiers differ", []uint64{100000, 20, 20, 20, 70}, nil}, // 70 is the next tier up
		{"deltas reach a tenth of the base", []uint64{1000, 40, 40, 20}, []group{{0, 3}}},
		{"too many deltas: the newest merge until 6 remain", []uint64{100000, 1, 100, 1, 100, 1, 100, 1, 100}, []group{{6, 8}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pol.plan(tt.sizes); !slices.Equal(got, tt.want) {
				t.Errorf("plan(%v) = %v, want %v", tt.sizes, got, tt.want)
			}
		})
	}
}

// apply merges sizes by groups, as compact does to segments.
func apply(sizes []uint64, groups []group) []uint64 {
	var out []uint64
	next := 0
	for _, g := range groups {
		out = append(out, sizes[next:g.lo]...)
		var s uint64
		for _, v := range sizes[g.lo : g.hi+1] {
			s += v
		}
		out = append(out, s)
		next = g.hi + 1
	}
	return append(out, sizes[next:]...)
}

// TestTierPolicyBoundsSegmentsAndRewrites adds 1,000 equal batches and
// checks what the policy promises: a few deltas at any time, the base
// rewritten once per tenth of growth, and each added posting rewritten a
// few times among deltas, not once per later add.
func TestTierPolicyBoundsSegmentsAndRewrites(t *testing.T) {
	pol := tierPolicy{FanIn: 4, BaseFraction: 0.1, MaxDeltas: 12}
	const batch = 1000
	sizes := []uint64{1_000_000}
	most, baseRewrites, total := 0, 0, 0
	var deltaRewritten uint64
	for range 1000 {
		sizes = append(sizes, batch)
		groups := pol.plan(sizes)
		for _, g := range groups {
			if g.lo == 0 {
				baseRewrites++
				continue
			}
			for _, v := range sizes[g.lo : g.hi+1] {
				deltaRewritten += v
			}
		}
		sizes = apply(sizes, groups)
		most = max(most, len(sizes)-1)
		total += len(sizes) - 1
	}
	// At most FanIn-1 = 3 per tier, and the base (2M postings at the end)
	// leaves room for log4(0.1 x 2M / 1000), about 4 tiers, below a tenth.
	if most > 12 {
		t.Errorf("up to %d deltas at once, want at most 12", most)
	}
	if mean := float64(total) / 1000; mean > 6 {
		t.Errorf("%.1f deltas on average, want at most 6", mean)
	}
	// The base grows from 1M to 2M postings: about ln 2 / ln 1.1 = 7 rewrites.
	if baseRewrites < 6 || baseRewrites > 9 {
		t.Errorf("%d base rewrites, want about 7", baseRewrites)
	}
	if perPosting := float64(deltaRewritten) / (1000 * batch); perPosting > 3 {
		t.Errorf("each added posting rewritten %.1f times among deltas, want at most 3", perPosting)
	}
}

// TestCompactKeepsAnswers adds songs one at a time with tiered compaction
// after each add: segments merge, and every song is still found with the
// score a fully merged index gives it.
func TestCompactKeepsAnswers(t *testing.T) {
	files := songs(t, 6)
	dir := t.TempDir()
	cfg := config{out: dir, extra: list(t, files[:2]), partitions: []int{1}, encoding: segment.EncodingVarint, workers: 2}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	pol := tierPolicy{FanIn: 2, BaseFraction: 100, MaxDeltas: 2}
	for _, f := range files[2:] {
		if err := addSongs(context.Background(), cfg, dir, list(t, []string{f})); err != nil {
			t.Fatal(err)
		}
		if err := compact(dir, segment.EncodingVarint, pol.plan); err != nil {
			t.Fatal(err)
		}
		m, err := versioned.Latest(dir)
		if err != nil {
			t.Fatal(err)
		}
		if d := len(m.Partitions[0]) - 1; d > 2 {
			t.Fatalf("version %d keeps %d deltas, policy allows 2", m.Version, d)
		}
	}
	tiered, err := versioned.Latest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := compact(dir, segment.EncodingVarint, mergeAll); err != nil {
		t.Fatal(err)
	}
	merged, err := versioned.Latest(dir)
	if err != nil || len(merged.Partitions[0]) != 1 {
		t.Fatalf("after -merge: %+v (%v)", merged, err)
	}
	for want, f := range files {
		got, score := identify(t, dir, tiered.Version, f)
		mgot, mscore := identify(t, dir, merged.Version, f)
		if got != uint32(want) || got != mgot || score != mscore {
			t.Errorf("%s: tiered index says track %d (score %d), merged says %d (score %d), want %d",
				filepath.Base(f), got, score, mgot, mscore, want)
		}
	}
}
