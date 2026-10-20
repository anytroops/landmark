package match

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/anytroops/landmark/internal/segment"
	"github.com/anytroops/landmark/internal/triplet"
)

const goldenB2Dir = "../../testdata/golden_b2"

type goldenLandmarks struct {
	Hashes []uint32 `json:"hashes"`
	Times  []uint32 `json:"times"`
	Spans  []uint8  `json:"spans"`
	Bands  []uint8  `json:"bands"`
}

func (g goldenLandmarks) landmarks(t *testing.T) []triplet.Landmark {
	t.Helper()
	if len(g.Times) != len(g.Hashes) || len(g.Spans) != len(g.Hashes) || len(g.Bands) != len(g.Hashes) {
		t.Fatal("golden landmark arrays differ in length")
	}
	out := make([]triplet.Landmark, len(g.Hashes))
	for i := range out {
		out[i] = triplet.Landmark{Hash: g.Hashes[i], T: g.Times[i], Span: g.Spans[i], Band: g.Bands[i]}
	}
	return out
}

type goldenTriplet struct {
	Index  []string `json:"index"`
	Params struct {
		ScaleRatio      float64 `json:"scale_ratio"`
		BinMin          int     `json:"bin_min"`
		BinMax          int     `json:"bin_max"`
		TableBins       int     `json:"table_bins"`
		OffsetFrames    int     `json:"offset_frames"`
		NearFrames      int     `json:"near_frames"`
		InlierFrames    float64 `json:"inlier_frames"`
		MaxBinsPerMatch int     `json:"max_bins_per_match"`
		PruneTracks     int     `json:"prune_tracks"`
	} `json:"params"`
	Queries []struct {
		goldenLandmarks
		Name            string             `json:"name"`
		RefineTracks    int                `json:"refine_tracks"`
		PruneTracks     int                `json:"prune_tracks"`
		TopK            int                `json:"top_k"`
		PostingsScanned uint64             `json:"postings_scanned"`
		Votes           uint64             `json:"votes"`
		Candidates      []TripletCandidate `json:"candidates"`
	} `json:"queries"`
}

func loadGoldenTriplet(t *testing.T) goldenTriplet {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenB2Dir, "match.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var g goldenTriplet
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	p := g.Params
	if p.ScaleRatio != scaleRatio || p.BinMin != binMin || p.BinMax != binMax || p.TableBins != tableBins ||
		p.OffsetFrames != offsetFrames || p.NearFrames != nearFrames || p.InlierFrames != inlierFrames ||
		p.MaxBinsPerMatch != maxBinsPerMatch || p.PruneTracks != PruneTracks {
		t.Fatalf("golden params %+v differ from the Go constants; regenerate with python -m prototype.golden_b2_match", p)
	}
	return g
}

// goldenSegment writes the golden index (track d = g.Index[d]) as a version 2
// segment and opens it.
func goldenSegment(t *testing.T, g goldenTriplet, enc segment.Encoding) *segment.Segment {
	t.Helper()
	var entries []segment.Entry
	for d, name := range g.Index {
		b, err := os.ReadFile(filepath.Join(goldenB2Dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var lm goldenLandmarks
		if err := json.Unmarshal(b, &lm); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, l := range lm.landmarks(t) {
			entries = append(entries, segment.Entry{Hash: l.Hash, Track: uint32(d), T: l.T, Span: l.Span, Band: l.Band})
		}
	}
	path := filepath.Join(t.TempDir(), "b2.lmrk")
	meta := segment.Meta{NPartitions: 1, NTracks: uint32(len(g.Index)), Encoding: enc, Aux: true}
	if err := segment.Write(path, meta, entries); err != nil {
		t.Fatalf("write segment: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read segment: %v", err)
	}
	seg, err := segment.FromBytes(b)
	if err != nil {
		t.Fatalf("open segment: %v", err)
	}
	return seg
}

// TestTripletParity requires the Python reference's results bit for bit.
func TestTripletParity(t *testing.T) {
	g := loadGoldenTriplet(t)
	for _, enc := range []segment.Encoding{segment.EncodingVarint, segment.EncodingFixed} {
		seg := goldenSegment(t, g, enc)
		m := NewTripletMatcher()
		for round := range 2 { // the second round reuses every buffer
			for _, q := range g.Queries {
				res, err := m.search(seg, q.landmarks(t), q.TopK, q.RefineTracks, q.PruneTracks)
				if err != nil {
					t.Fatalf("%s: %v", q.Name, err)
				}
				if res.PostingsScanned != q.PostingsScanned || res.Votes != q.Votes {
					t.Errorf("enc %d round %d %s: scanned %d votes %d, Python %d and %d",
						enc, round, q.Name, res.PostingsScanned, res.Votes, q.PostingsScanned, q.Votes)
				}
				if len(res.Candidates) != len(q.Candidates) {
					t.Fatalf("enc %d %s: %d candidates, Python %d", enc, q.Name, len(res.Candidates), len(q.Candidates))
				}
				for i, c := range res.Candidates {
					w := q.Candidates[i]
					if c.Track != w.Track || c.Score != w.Score ||
						math.Float64bits(c.Offset) != math.Float64bits(w.Offset) ||
						math.Float64bits(c.Scale) != math.Float64bits(w.Scale) {
						t.Errorf("enc %d %s candidate %d = %+v, Python %+v", enc, q.Name, i, c, w)
					}
				}
			}
		}
	}
}

func TestVoteWindows(t *testing.T) {
	m := NewTripletMatcher()
	tests := []struct {
		name        string
		sr, sq      int
		first, last int
		empty       bool
	}{
		{name: "long equal spans reach the bins of +/-1.6%", sr: 63, sq: 63, first: -1, last: 1},
		{name: "short equal spans are capped to 4 bins", sr: 10, sq: 10, first: -1, last: 2},
		{name: "ratio 1.04 known to +/-2%", sr: 52, sq: 50, first: 0, last: 3},
		{name: "ratio 0.67 is below the searched range", sr: 2, sq: 3, empty: true},
		{name: "zero query span", sr: 5, sq: 0, empty: true},
		{name: "zero reference span", sr: 0, sq: 5, empty: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := tt.sr<<8 | tt.sq
			f, l := int(m.first[w]), int(m.last[w])
			if tt.empty {
				if f <= l {
					t.Errorf("window %d..%d, want empty", f, l)
				}
				return
			}
			if f != tt.first || l != tt.last {
				t.Errorf("window %d..%d, want %d..%d", f, l, tt.first, tt.last)
			}
		})
	}
	for w := range m.first {
		if f, l := m.first[w], m.last[w]; f <= l && (f < binMin || l > binMax) {
			t.Fatalf("spans %d/%d vote in bins %d..%d outside %d..%d", w>>8, w&0xFF, f, l, binMin, binMax)
		}
	}
}

func TestFitLineIsExactOnIntegerLines(t *testing.T) {
	pts := func(xs []int64, f func(int64) int64) []fitPoint {
		out := make([]fitPoint, len(xs))
		for i, x := range xs {
			out[i] = fitPoint{x: x, y: f(x)}
		}
		return out
	}
	xs := []int64{0, 5, 9, 20}
	if s, a, ok := fitLine(pts(xs, func(x int64) int64 { return 300 + x }), nil); !ok || s != 1 || a != 300 {
		t.Errorf("y = 300 + x: got (%v, %v, %v)", s, a, ok)
	}
	if s, a, ok := fitLine(pts(xs, func(x int64) int64 { return 7 + 2*x }), nil); !ok || s != 2 || a != 7 {
		t.Errorf("y = 7 + 2x: got (%v, %v, %v)", s, a, ok)
	}
	if _, _, ok := fitLine(pts([]int64{4, 4}, func(x int64) int64 { return x }), nil); ok {
		t.Error("constant x: want no fit")
	}
	if _, _, ok := fitLine(nil, nil); ok {
		t.Error("no points: want no fit")
	}
}

func TestTripletSearchEmpty(t *testing.T) {
	g := loadGoldenTriplet(t)
	seg := goldenSegment(t, g, segment.EncodingVarint)
	res, err := NewTripletMatcher().Search(seg, nil, 10)
	if err != nil || len(res.Candidates) != 0 || res.PostingsScanned != 0 {
		t.Errorf("empty query: %+v, %v", res, err)
	}
	res, err = NewTripletMatcher().Search(seg, []triplet.Landmark{{Hash: 0xFFFFFFFF, T: 3, Span: 9}}, 10)
	if err != nil || len(res.Candidates) != 0 || res.PostingsScanned != 0 {
		t.Errorf("unknown hash: %+v, %v", res, err)
	}
}

func TestTripletSearchRejectsPairSegment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.lmrk")
	meta := segment.Meta{NPartitions: 1, NTracks: 1, Encoding: segment.EncodingVarint}
	if err := segment.Write(path, meta, []segment.Entry{{Hash: 7, Track: 0, T: 1}}); err != nil {
		t.Fatal(err)
	}
	seg, err := segment.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = seg.Close() }()
	if _, err := NewTripletMatcher().Search(seg, []triplet.Landmark{{Hash: 7, T: 0, Span: 9}}, 10); err == nil {
		t.Error("triplet search over a version 1 segment: want an error")
	}
}

func TestFlatFitIsNotAMatch(t *testing.T) {
	// One posting per hash; the query repeats each hash at many times, so
	// every match has the same reference time: a flat line, scale 0.
	var entries []segment.Entry
	var query []triplet.Landmark
	for h := uint32(1); h <= 20; h++ {
		entries = append(entries, segment.Entry{Hash: h, Track: 0, T: 500, Span: 30, Band: 50})
		for q := uint32(0); q < 25; q += 5 {
			query = append(query, triplet.Landmark{Hash: h, T: q, Span: 30, Band: 50})
		}
	}
	path := filepath.Join(t.TempDir(), "flat.lmrk")
	meta := segment.Meta{NPartitions: 1, NTracks: 1, Encoding: segment.EncodingVarint, Aux: true}
	if err := segment.Write(path, meta, entries); err != nil {
		t.Fatal(err)
	}
	seg, err := segment.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = seg.Close() }()
	res, err := NewTripletMatcher().Search(seg, query, 10)
	if err != nil || len(res.Candidates) != 0 {
		t.Fatalf("flat fit: %+v, %v; want no candidates", res, err)
	}
}

func TestPruneKeepsTheTracksWithMostMatches(t *testing.T) {
	m := NewTripletMatcher()
	for _, tr := range []uint32{5, 5, 5, 2, 2, 9, 9, 7, 2, 9} { // 5, 2, 9: 3 each; 7: 1
		m.matches = append(m.matches, tripletMatch{track: tr})
	}
	m.prune(2)
	var got []uint32
	for _, x := range m.matches {
		got = append(got, x.track)
	}
	if want := []uint32{5, 5, 5, 2, 2, 2}; !slices.Equal(got, want) { // three-way tie: smallest ids win
		t.Fatalf("kept tracks %v, want %v", got, want)
	}
	for _, c := range m.counts {
		if c != 0 {
			t.Fatal("counts not reset for the next search")
		}
	}
}
