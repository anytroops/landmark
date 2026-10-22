package match

import (
	"fmt"
	"math"
	"math/rand/v2"
	"path/filepath"
	"testing"

	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/segment"
)

// split writes entries as one segment and as a base plus deltas cut at the
// given local track ids (D42), and opens both.
func split(t *testing.T, entries []segment.Entry, meta segment.Meta, cuts []uint32) (*segment.Segment, *segment.Set) {
	t.Helper()
	dir := t.TempDir()
	open := func(name string, m segment.Meta, es []segment.Entry) *segment.Segment {
		path := filepath.Join(dir, name)
		if err := segment.Write(path, m, es); err != nil {
			t.Fatal(err)
		}
		seg, err := segment.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = seg.Close() })
		return seg
	}
	whole := open("whole.lmrk", meta, entries)
	bounds := append(append([]uint32{0}, cuts...), meta.NTracks)
	var members []*segment.Segment
	for i := 1; i < len(bounds); i++ {
		var part []segment.Entry
		for _, e := range entries {
			if e.Track >= bounds[i-1] && e.Track < bounds[i] {
				part = append(part, e)
			}
		}
		m := meta
		m.NTracks = bounds[i] // the id range so far
		members = append(members, open(fmt.Sprintf("member%d.lmrk", i), m, part))
	}
	set, err := segment.NewSet(members)
	if err != nil {
		t.Fatal(err)
	}
	return whole, set
}

func TestSetSearchesLikeOneSegmentPairs(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	const tracks = 60
	var entries []segment.Entry
	for range 20000 {
		entries = append(entries, segment.Entry{Hash: uint32(rng.IntN(500)), Track: uint32(rng.IntN(tracks)), T: uint32(rng.IntN(2000))})
	}
	for _, enc := range []segment.Encoding{segment.EncodingVarint, segment.EncodingFixed} {
		meta := segment.Meta{Partition: 1, NPartitions: 4, NTracks: tracks, Encoding: enc}
		whole, set := split(t, entries, meta, []uint32{40, 52})
		var mw, ms Matcher
		for q := range 50 {
			var query []fingerprint.Landmark
			for range 300 {
				query = append(query, fingerprint.Landmark{Hash: uint32(rng.IntN(500)), T: uint32(rng.IntN(400))})
			}
			a, err := mw.Search(whole, query, 20)
			if err != nil {
				t.Fatal(err)
			}
			b, err := ms.Search(set, query, 20)
			if err != nil {
				t.Fatal(err)
			}
			if a.PostingsScanned != b.PostingsScanned || len(a.Candidates) != len(b.Candidates) {
				t.Fatalf("enc %d query %d: whole %+v, set %+v", enc, q, a, b)
			}
			for i := range a.Candidates {
				if a.Candidates[i] != b.Candidates[i] {
					t.Fatalf("enc %d query %d candidate %d: whole %+v, set %+v", enc, q, i, a.Candidates[i], b.Candidates[i])
				}
			}
		}
		if whole.GlobalTrack(57) != set.GlobalTrack(57) || set.NTracks() != tracks {
			t.Errorf("set ids: global %d vs %d, tracks %d", whole.GlobalTrack(57), set.GlobalTrack(57), set.NTracks())
		}
	}
}

// TestSetSearchesLikeOneSegmentTriplets splits the golden triplet index and
// requires the golden queries' answers bit for bit.
func TestSetSearchesLikeOneSegmentTriplets(t *testing.T) {
	g := loadGoldenTriplet(t)
	whole := goldenSegment(t, g, segment.EncodingVarint)
	var entries []segment.Entry
	for h := 0; h < whole.NKeys(); h++ {
		hash := whole.Key(h)
		ps, err := whole.AppendAuxPostings(nil, hash)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ps {
			entries = append(entries, segment.Entry{Hash: hash, Track: p.Track, T: p.T, Span: p.Span, Band: p.Band})
		}
	}
	n := whole.NTracks()
	meta := segment.Meta{NPartitions: 1, NTracks: n, Encoding: segment.EncodingVarint, Aux: true}
	_, set := split(t, entries, meta, []uint32{n / 2, n - 1})
	mw, ms := NewTripletMatcher(), NewTripletMatcher()
	for _, q := range g.Queries {
		a, err := mw.search(whole, q.landmarks(t), q.TopK, q.RefineTracks, q.PruneTracks)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ms.search(set, q.landmarks(t), q.TopK, q.RefineTracks, q.PruneTracks)
		if err != nil {
			t.Fatal(err)
		}
		if a.PostingsScanned != b.PostingsScanned || a.Votes != b.Votes || len(a.Candidates) != len(b.Candidates) {
			t.Fatalf("%s: whole scanned %d votes %d (%d candidates), set %d %d (%d)", q.Name,
				a.PostingsScanned, a.Votes, len(a.Candidates), b.PostingsScanned, b.Votes, len(b.Candidates))
		}
		for i, c := range a.Candidates {
			d := b.Candidates[i]
			if c.Track != d.Track || c.Score != d.Score || math.Float64bits(c.Offset) != math.Float64bits(d.Offset) ||
				math.Float64bits(c.Scale) != math.Float64bits(d.Scale) {
				t.Errorf("%s candidate %d: whole %+v, set %+v", q.Name, i, c, d)
			}
		}
	}
}
