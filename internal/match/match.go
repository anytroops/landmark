// Package match scores tracks for a query by offset-histogram voting.
//
// For each query landmark (h, tq) and each posting (track, tdb) under h, vote
// for (track, tdb - tq). A track's score is its largest vote count: how many
// landmarks agree on one time alignment.
//
// Votes are counted by sorting, not hashing (D22): each vote is packed into
// one uint64, the slice is radix-sorted (D27), and equal runs are counted. The result
// matches prototype/match.py exactly, including tie-breaking, and buffers are
// reused across queries so a query allocates almost nothing.
package match

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/segment"
)

// offsetBias shifts a signed offset into the low 32 bits of a vote.
const offsetBias = 1 << 31

// Source is anything that can list the postings for a hash.
// *segment.Segment implements it.
type Source interface {
	AppendPostings(dst []segment.Posting, hash uint32) ([]segment.Posting, error)
}

// Candidate is one scored track. Track is the id as stored in the Source.
type Candidate struct {
	Track  uint32
	Score  uint32
	Offset int32 // tdb - tq of the best alignment, in frames
}

// Result is the outcome of one search.
type Result struct {
	Candidates      []Candidate // descending score; ties by ascending track
	PostingsScanned uint64
}

// Matcher holds reusable buffers. It is not safe for concurrent use; give
// each goroutine its own (or pool them).
type Matcher struct {
	votes    []uint64
	scratch  []uint64 // radix sort buffer; swaps roles with votes
	postings []segment.Posting
	best     []Candidate
}

// Search scores every track that shares a hash with the query and returns
// the topK best.
func (m *Matcher) Search(src Source, query []fingerprint.Landmark, topK int) (Result, error) {
	m.votes = m.votes[:0]
	var scanned uint64
	prevHash, havePrev := uint32(0), false
	for _, q := range query {
		// FingerprintQuery output is sorted by hash, so repeats are adjacent:
		// reuse the postings instead of searching the segment again.
		if !havePrev || q.Hash != prevHash {
			var err error
			if m.postings, err = src.AppendPostings(m.postings[:0], q.Hash); err != nil {
				return Result{}, fmt.Errorf("match: %w", err)
			}
			prevHash, havePrev = q.Hash, true
		}
		scanned += uint64(len(m.postings))
		for _, p := range m.postings {
			off := int64(p.T) - int64(q.T) + offsetBias
			m.votes = append(m.votes, uint64(p.Track)<<32|uint64(uint32(off)))
		}
	}
	if cap(m.scratch) < len(m.votes) {
		m.scratch = make([]uint64, len(m.votes), cap(m.votes))
	}
	sorted := sortVotes(m.votes, m.scratch[:len(m.votes)])
	if len(sorted) > 0 && &sorted[0] != &m.votes[0] { // landed in scratch: swap so both stay reusable
		m.votes, m.scratch = sorted, m.votes
	}

	// One pass over sorted votes. Within a track, runs come in ascending
	// offset order; keeping only strictly larger counts makes ties go to the
	// smallest offset, as numpy's stable lexsort does in the prototype.
	m.best = m.best[:0]
	for i := 0; i < len(m.votes); {
		j := i
		for j < len(m.votes) && m.votes[j] == m.votes[i] {
			j++
		}
		track := uint32(m.votes[i] >> 32)
		off := int32(int64(uint32(m.votes[i])) - offsetBias)
		n := uint32(j - i)
		if k := len(m.best) - 1; k >= 0 && m.best[k].Track == track {
			if n > m.best[k].Score {
				m.best[k].Score, m.best[k].Offset = n, off
			}
		} else {
			m.best = append(m.best, Candidate{Track: track, Score: n, Offset: off})
		}
		i = j
	}

	// best is in ascending track order; a stable sort by score keeps that
	// order among equal scores.
	slices.SortStableFunc(m.best, func(a, b Candidate) int { return cmp.Compare(b.Score, a.Score) })
	k := min(topK, len(m.best))
	return Result{Candidates: slices.Clone(m.best[:k]), PostingsScanned: scanned}, nil
}

// RunnerUp returns the best score among candidates outside the top
// candidate's duplicate cluster (D14). clusterOf maps a track to its
// cluster representative. Correct when the candidate list is longer than
// the largest cluster.
func RunnerUp(cands []Candidate, clusterOf func(uint32) uint32) uint32 {
	if len(cands) == 0 {
		return 0
	}
	top := clusterOf(cands[0].Track)
	for _, c := range cands[1:] {
		if clusterOf(c.Track) != top {
			return c.Score
		}
	}
	return 0
}

// Accept reports whether the top score clears tAbs and beats the runner-up
// by tRatio.
func Accept(top, runnerUp, tAbs uint32, tRatio float64) bool {
	return top >= tAbs && float64(top) >= tRatio*float64(max(runnerUp, 1))
}
