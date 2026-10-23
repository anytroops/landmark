package main

import (
	"fmt"
	"log/slog"
	"math"
	"time"

	versioned "github.com/anytroops/landmark/internal/manifest"
	"github.com/anytroops/landmark/internal/segment"
)

// tierPolicy decides which of a partition's segments to merge (D47,
// size-tiered merging): adding songs makes small delta segments, and each
// segment costs every search a lookup (BENCHMARKS B15), while rewriting the
// base costs time in proportion to the whole partition.
//
//  1. When the deltas together hold BaseFraction of the base's postings,
//     they all fold into the base: the base is rewritten once per
//     BaseFraction of growth, not once per add.
//  2. Otherwise FanIn adjacent deltas of the same size tier (postings within
//     a factor of FanIn: floor(log_FanIn postings) equal) merge into one,
//     newest first, repeating while merges make new full tiers: like a
//     counter in base FanIn, at most FanIn-1 deltas per tier.
//  3. If more than MaxDeltas deltas still remain (sizes that never line up
//     in a tier), the newest FanIn merge anyway, until MaxDeltas remain.
type tierPolicy struct {
	FanIn        int
	BaseFraction float64
	MaxDeltas    int
}

// group is a run of adjacent members, lo to hi inclusive (0 is the base),
// that becomes one segment.
type group struct{ lo, hi int }

// plan returns the merges for members of the given sizes (postings, base
// first) as groups of the original members; members in no group stay.
// Merges always join adjacent runs, so each group is contiguous and
// track counts stay in order along the set.
func (p tierPolicy) plan(sizes []uint64) []group {
	if len(sizes) < 2 {
		return nil
	}
	var deltas uint64
	for _, s := range sizes[1:] {
		deltas += s
	}
	if float64(deltas) >= p.BaseFraction*float64(sizes[0]) {
		return []group{{0, len(sizes) - 1}}
	}
	// Simulate on the deltas: each element is a run of original members.
	type run struct {
		size   uint64
		lo, hi int
	}
	runs := make([]run, len(sizes)-1)
	for i, s := range sizes[1:] {
		runs[i] = run{s, i + 1, i + 1}
	}
	tier := func(s uint64) int {
		if s <= 1 {
			return 0
		}
		return int(math.Floor(math.Log(float64(s)) / math.Log(float64(p.FanIn))))
	}
	mergeTail := func(n int) {
		tail := runs[len(runs)-n:]
		m := run{lo: tail[0].lo, hi: tail[n-1].hi}
		for _, r := range tail {
			m.size += r.size
		}
		runs = append(runs[:len(runs)-n], m)
	}
	for {
		merged := false
		// The newest FanIn runs share a tier: merge them (a merge can
		// complete the tier above, so look again).
		if n := p.FanIn; len(runs) >= n {
			t := tier(runs[len(runs)-1].size)
			same := true
			for _, r := range runs[len(runs)-n:] {
				same = same && tier(r.size) == t
			}
			if same {
				mergeTail(n)
				merged = true
			}
		}
		if !merged && len(runs) > p.MaxDeltas {
			mergeTail(min(p.FanIn, len(runs)-p.MaxDeltas+1))
			merged = true
		}
		if !merged {
			break
		}
	}
	var out []group
	for _, r := range runs {
		if r.hi > r.lo {
			out = append(out, group{r.lo, r.hi})
		}
	}
	return out
}

// compact applies plan to every partition of the index in dir and, if
// anything merges, publishes the result as the next version. Ids and
// postings are unchanged, so searches answer exactly as before.
func compact(dir string, enc segment.Encoding, plan func(sizes []uint64) []group) error {
	start := time.Now()
	cur, err := versioned.Latest(dir)
	if err != nil {
		return err
	}
	catalog, extras, err := readCatalog(dir, cur)
	if err != nil {
		return err
	}
	next := cur
	next.Version = cur.Version + 1
	next.Partitions = make([][]versioned.Member, cur.NPartitions)
	next.Catalog = fmt.Sprintf("catalog-%08d.json", next.Version)
	next.Extras = fmt.Sprintf("extras-%08d.json", next.Version)
	written := 0
	for p := range cur.NPartitions {
		members, n, err := compactPartition(dir, cur, p, next.Version, enc, plan)
		if err != nil {
			return err
		}
		next.Partitions[p], written = members, written+n
	}
	if written == 0 {
		slog.Info("nothing to merge", "dir", dir, "version", cur.Version)
		return nil
	}
	if err := publish(dir, next, catalog, extras); err != nil {
		return err
	}
	var segs []int
	for _, ms := range next.Partitions {
		segs = append(segs, len(ms))
	}
	slog.Info("merged segments", "dir", dir, "version", next.Version, "written", written,
		"segments_per_partition", segs, "wall_s", fmt.Sprintf("%.1f", time.Since(start).Seconds()))
	return nil
}

// compactPartition writes partition p's merged groups and returns its
// members for the next version and how many segments it wrote.
func compactPartition(dir string, cur versioned.Manifest, p uint32, version uint64, enc segment.Encoding,
	plan func(sizes []uint64) []group) ([]versioned.Member, int, error) {
	old := cur.Partitions[p]
	if len(old) < 2 {
		return old, 0, nil
	}
	set, err := versioned.Open(dir, cur, p)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = set.Close() }()
	segs := set.Segments()
	sizes := make([]uint64, len(segs))
	for i, s := range segs {
		sizes[i] = s.NPostings()
	}
	groups := plan(sizes)
	if len(groups) == 0 {
		return old, 0, nil
	}
	var out []versioned.Member
	next := 0
	for k, g := range groups {
		out = append(out, old[next:g.lo]...)
		sub, err := segment.NewSet(segs[g.lo : g.hi+1]) // shares set's segments: not closed on its own
		if err != nil {
			return nil, 0, err
		}
		entries, err := allEntries(sub)
		if err != nil {
			return nil, 0, err
		}
		name := fmt.Sprintf("part-%03d.v%08d.lmrk", p, version) // a new base keeps the usual name
		if g.lo > 0 {
			name = fmt.Sprintf("part-%03d.v%08d.m%02d.lmrk", p, version, k)
		}
		meta := segment.Meta{Partition: p, NPartitions: cur.NPartitions, NTracks: old[g.hi].NTracks,
			Encoding: enc, Aux: set.HasAux()}
		mem, err := writeMember(dir, name, meta, entries)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, mem)
		next = g.hi + 1
	}
	return append(out, old[next:]...), len(groups), nil
}

// mergeAll is the plan that folds everything into the base (-merge).
func mergeAll(sizes []uint64) []group {
	if len(sizes) < 2 {
		return nil
	}
	return []group{{0, len(sizes) - 1}}
}
