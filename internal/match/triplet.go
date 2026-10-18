package match

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/anytroops/landmark/internal/segment"
	"github.com/anytroops/landmark/internal/triplet"
)

// Triplet matching (D31 option B2, D33) is a port of prototype/b2_match.py,
// which documents the algorithm: coarse votes in (scale bin, offset bin) per
// track, then a least-squares line fit for the best tracks. It must return
// the same bits as the Python reference (tested against
// testdata/golden_b2/match.json), so it follows its arithmetic exactly:
// scales come from a table built by repeated multiplication, line fits from
// exact integer sums, and every product that feeds an addition is wrapped in
// float64() so the compiler cannot fuse the two into an FMA, which would
// round once instead of twice.
const (
	scaleRatio      = 1.02 // coarse scale bin b is the scale scaleRatio^b
	binMin, binMax  = -9, 8
	tableBins       = 320 // the scale table covers |b| <= 320
	offsetFrames    = 6
	nearFrames      = 2 * offsetFrames
	inlierFrames    = 2.0
	maxBinsPerMatch = 4 // even: a capped window is floorBin(ratio) - 1 .. + 2

	// RefineTracks is how many tracks get a line fit.
	RefineTracks = 20

	// maxTripletTrack is the largest local track id a vote key can hold.
	maxTripletTrack = 1<<24 - 1
)

// AuxSource lists postings with their span and band. A version 2
// *segment.Segment implements it.
type AuxSource interface {
	AppendAuxPostings(dst []segment.AuxPosting, hash uint32) ([]segment.AuxPosting, error)
}

// TripletCandidate is one scored track of a triplet search.
type TripletCandidate struct {
	Track  uint32
	Score  uint32  // inlier query landmarks
	Offset float64 // reference frame at query frame 0
	Scale  float64 // reference time per query time (> 1: the query plays faster)
}

// TripletResult is the outcome of one triplet search.
type TripletResult struct {
	Candidates      []TripletCandidate // descending score; ties by ascending track
	PostingsScanned uint64
	Votes           uint64
}

// tripletMatch is one (query landmark, posting) pair with equal hashes.
type tripletMatch struct {
	qi     int32 // query landmark index
	track  uint32
	tq, tr uint32
	sq, sr uint8
}

type fitPoint struct {
	x, y int64 // query frame, reference frame
	qi   int32
}

// coarseBest is a track's best coarse bin: its vote key and count.
type coarseBest struct {
	track uint32
	count uint32
	key   uint64
}

// TripletMatcher holds its tables and reusable buffers. It is not safe for
// concurrent use; give each goroutine its own (or pool them).
type TripletMatcher struct {
	table       []float64 // scale of bin b at table[b+tableBins]
	first, last []int8    // vote window per span pair, at [span_ref<<8 | span_query]

	matches  []tripletMatch
	votes    []uint64
	scratch  []uint64
	postings []segment.AuxPosting
	best     []coarseBest
	slot     []int8 // per local track: refine slot, or -1
	points   [][]fitPoint
}

// NewTripletMatcher returns a matcher with its tables built (~128 KB).
func NewTripletMatcher() *TripletMatcher {
	m := &TripletMatcher{table: scaleTable()}
	m.first, m.last = voteWindows(m.table)
	return m
}

// scaleTable returns scaleRatio^b at index b+tableBins, by repeated
// multiplication and division from 1, as prototype.b2_match.scale_table.
func scaleTable() []float64 {
	t := make([]float64, 2*tableBins+1)
	t[tableBins] = 1
	for i := 1; i <= tableBins; i++ {
		t[tableBins+i] = t[tableBins+i-1] * scaleRatio
		t[tableBins-i] = t[tableBins-i+1] / scaleRatio
	}
	return t
}

// floorBin is the largest b with table scale <= r.
func floorBin(table []float64, r float64) int {
	return sort.Search(len(table), func(i int) bool { return table[i] > r }) - 1 - tableBins
}

// ceilBin is the smallest b with table scale >= r.
func ceilBin(table []float64, r float64) int {
	return sort.Search(len(table), func(i int) bool { return table[i] >= r }) - tableBins
}

// voteWindows returns, for each (span_ref, span_query) in 0..255, the first
// and last coarse scale bin a match votes in, as prototype.b2_match.windows.
// first > last means no votes: a zero span, or bins outside binMin..binMax.
func voteWindows(table []float64) (first, last []int8) {
	first, last = make([]int8, 1<<16), make([]int8, 1<<16)
	for sr := range 256 {
		for sq := range 256 {
			w := sr<<8 | sq
			if sr == 0 || sq == 0 {
				first[w], last[w] = 1, 0
				continue
			}
			r, q := float64(sr), float64(sq)
			lo := math.Max(r-0.5, 0.5) / (q + 0.5)
			hi := (r + 0.5) / math.Max(q-0.5, 0.5)
			f, l := floorBin(table, lo), ceilBin(table, hi)
			if l-f+1 > maxBinsPerMatch {
				f = floorBin(table, r/q) - (maxBinsPerMatch/2 - 1)
				l = f + maxBinsPerMatch - 1
			}
			f, l = max(f, binMin), min(l, binMax)
			if f > l {
				f, l = 1, 0
			}
			first[w], last[w] = int8(f), int8(l)
		}
	}
	return first, last
}

// Search scores every track that shares a hash with the query and returns
// the topK best.
func (m *TripletMatcher) Search(src AuxSource, query []triplet.Landmark, topK int) (TripletResult, error) {
	return m.search(src, query, topK, RefineTracks)
}

func (m *TripletMatcher) search(src AuxSource, query []triplet.Landmark, topK, refine int) (TripletResult, error) {
	refine = min(refine, math.MaxInt8) // refine slots are int8
	if err := m.collect(src, query); err != nil {
		return TripletResult{}, err
	}
	res := TripletResult{PostingsScanned: uint64(len(m.matches))}
	m.vote()
	res.Votes = uint64(len(m.votes))
	m.pickTracks(refine)
	res.Candidates = m.refine()
	slices.SortFunc(res.Candidates, func(a, b TripletCandidate) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score), cmp.Compare(a.Track, b.Track))
	})
	if len(res.Candidates) > topK {
		res.Candidates = res.Candidates[:topK]
	}
	return res, nil
}

// collect lists every (query landmark, posting) match, in query order.
func (m *TripletMatcher) collect(src AuxSource, query []triplet.Landmark) error {
	m.matches = m.matches[:0]
	prevHash, havePrev := uint32(0), false
	for i, q := range query {
		if !havePrev || q.Hash != prevHash { // repeats are adjacent in FingerprintQuery output
			var err error
			if m.postings, err = src.AppendAuxPostings(m.postings[:0], q.Hash); err != nil {
				return fmt.Errorf("match: %w", err)
			}
			prevHash, havePrev = q.Hash, true
		}
		for _, p := range m.postings {
			if p.Track > maxTripletTrack {
				return fmt.Errorf("match: local track %d exceeds %d", p.Track, maxTripletTrack)
			}
			m.matches = append(m.matches, tripletMatch{
				qi: int32(i), track: p.Track, tq: q.T, tr: p.T, sq: q.Span, sr: p.Span,
			})
		}
	}
	return nil
}

// vote fills m.votes, sorted, with one key per (match, scale bin):
// track<<40 | (bin - binMin)<<32 | (offset bin + 2^31).
func (m *TripletMatcher) vote() {
	m.votes = m.votes[:0]
	for _, x := range m.matches {
		w := int(x.sr)<<8 | int(x.sq)
		for b := int(m.first[w]); b <= int(m.last[w]); b++ {
			s := m.table[b+tableBins]
			off := math.Floor((float64(x.tr) - float64(s*float64(x.tq))) / offsetFrames)
			m.votes = append(m.votes,
				uint64(x.track)<<40|uint64(b-binMin)<<32|uint64(uint32(int64(off)+offsetBias)))
		}
	}
	if cap(m.scratch) < len(m.votes) {
		m.scratch = make([]uint64, len(m.votes), cap(m.votes))
	}
	sorted := sortVotes(m.votes, m.scratch[:len(m.votes)])
	if len(sorted) > 0 && &sorted[0] != &m.votes[0] {
		m.votes, m.scratch = sorted, m.votes
	}
}

// pickTracks sets m.best to the refine tracks with the most votes in one
// bin, by count then track id, each with its best bin (ties: smallest key).
func (m *TripletMatcher) pickTracks(refine int) {
	m.best = m.best[:0]
	for i := 0; i < len(m.votes); {
		j := i
		for j < len(m.votes) && m.votes[j] == m.votes[i] {
			j++
		}
		track, n := uint32(m.votes[i]>>40), uint32(j-i)
		if k := len(m.best) - 1; k >= 0 && m.best[k].track == track {
			if n > m.best[k].count {
				m.best[k].count, m.best[k].key = n, m.votes[i]
			}
		} else {
			m.best = append(m.best, coarseBest{track: track, count: n, key: m.votes[i]})
		}
		i = j
	}
	slices.SortStableFunc(m.best, func(a, b coarseBest) int { return cmp.Compare(b.count, a.count) })
	m.best = m.best[:min(refine, len(m.best))]
}

// refine fits a line for each picked track over its matches near the
// winning coarse bin and scores the inliers.
func (m *TripletMatcher) refine() []TripletCandidate {
	if len(m.best) == 0 {
		return nil
	}
	maxTrack := uint32(0)
	for _, b := range m.best {
		maxTrack = max(maxTrack, b.track)
	}
	for uint32(len(m.slot)) <= maxTrack {
		m.slot = append(m.slot, -1)
	}
	for len(m.points) < len(m.best) {
		m.points = append(m.points, nil)
	}
	scale := make([]float64, len(m.best))
	center := make([]float64, len(m.best)) // (offset bin + 0.5) * offsetFrames
	for s, b := range m.best {
		m.slot[b.track] = int8(s)
		m.points[s] = m.points[s][:0]
		scale[s] = m.table[int((b.key>>32)&0xFF)+binMin+tableBins]
		o := int64(b.key&0xFFFFFFFF) - offsetBias
		center[s] = (float64(o) + 0.5) * offsetFrames
	}
	defer func() {
		for _, b := range m.best {
			m.slot[b.track] = -1
		}
	}()
	for _, x := range m.matches {
		if x.track > maxTrack {
			continue
		}
		s := m.slot[x.track]
		if s < 0 {
			continue
		}
		resid := float64(x.tr) - (float64(scale[s]*float64(x.tq)) + center[s])
		if math.Abs(resid) <= nearFrames {
			m.points[s] = append(m.points[s], fitPoint{x: int64(x.tq), y: int64(x.tr), qi: x.qi})
		}
	}

	out := make([]TripletCandidate, 0, len(m.best))
	for s, b := range m.best {
		pts := m.points[s]
		sc, off, ok := fitLine(pts, nil)
		if !ok {
			continue
		}
		for range 2 {
			inlier := isInlier(sc, off)
			sc2, off2, ok := fitLine(pts, inlier)
			if !ok {
				break
			}
			sc, off = sc2, off2
		}
		inlier := isInlier(sc, off)
		score, prev := uint32(0), int32(-1)
		for _, p := range pts { // qi is nondecreasing: matches are in query order
			if inlier(p) && p.qi != prev {
				score++
				prev = p.qi
			}
		}
		out = append(out, TripletCandidate{Track: b.track, Score: score, Offset: off, Scale: sc})
	}
	return out
}

func isInlier(scale, offset float64) func(fitPoint) bool {
	return func(p fitPoint) bool {
		return math.Abs(float64(p.y)-(offset+float64(scale*float64(p.x)))) <= inlierFrames
	}
}

// fitLine returns the least-squares (scale, offset) of y = offset + scale*x
// over the points keep accepts (all if keep is nil), as
// prototype.b2_match.fit_line; ok is false when x is constant. Sums are
// exact in int64 for any realistic query (frames < 2^24, points < 2^14).
func fitLine(pts []fitPoint, keep func(fitPoint) bool) (scale, offset float64, ok bool) {
	var n, sx, sy, sxx, sxy int64
	for _, p := range pts {
		if keep != nil && !keep(p) {
			continue
		}
		n++
		sx += p.x
		sy += p.y
		sxx += p.x * p.x
		sxy += p.x * p.y
	}
	den := n*sxx - sx*sx
	if den <= 0 {
		return 0, 0, false
	}
	scale = float64(n*sxy-sx*sy) / float64(den)
	offset = (float64(sy) - float64(scale*float64(sx))) / float64(n)
	return scale, offset, true
}
