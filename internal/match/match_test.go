package match

import (
	"cmp"
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/segment"
)

const goldenDir = "../../testdata/golden"

// mapSource is a trivially correct Source for tests.
type mapSource map[uint32][]segment.Posting

func (m mapSource) AppendPostings(dst []segment.Posting, h uint32) ([]segment.Posting, error) {
	return append(dst, m[h]...), nil
}

// bruteForce is the reference: a map of (track, offset) -> votes, then the
// prototype's tie rules (best offset = smallest; rank ties = smallest track).
func bruteForce(src mapSource, query []fingerprint.Landmark) []Candidate {
	type key struct {
		track uint32
		off   int32
	}
	votes := map[key]uint32{}
	for _, q := range query {
		for _, p := range src[q.Hash] {
			votes[key{p.Track, int32(int64(p.T) - int64(q.T))}]++
		}
	}
	best := map[uint32]Candidate{}
	for k, n := range votes {
		c, ok := best[k.track]
		if !ok || n > c.Score || (n == c.Score && k.off < c.Offset) {
			best[k.track] = Candidate{Track: k.track, Score: n, Offset: k.off}
		}
	}
	out := slices.Collect(func(yield func(Candidate) bool) {
		for _, c := range best {
			if !yield(c) {
				return
			}
		}
	})
	slices.SortFunc(out, func(a, b Candidate) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score), cmp.Compare(a.Track, b.Track))
	})
	return out
}

func TestSearchMatchesBruteForce(t *testing.T) {
	for seed := range uint64(5) {
		rng := rand.New(rand.NewPCG(seed, 99))
		src := mapSource{}
		for range 3000 {
			h := rng.Uint32N(80)
			src[h] = append(src[h], segment.Posting{Track: rng.Uint32N(30), T: rng.Uint32N(400)})
		}
		var query []fingerprint.Landmark
		for range 300 {
			query = append(query, fingerprint.Landmark{Hash: rng.Uint32N(100), T: rng.Uint32N(400)})
		}
		var m Matcher
		got, err := m.Search(src, query, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if want := bruteForce(src, query); !slices.Equal(got.Candidates, want) {
			t.Fatalf("seed %d: got %v\nwant %v", seed, got.Candidates[:5], want[:5])
		}
	}
}

func TestSearchReusesBuffersSafely(t *testing.T) {
	src := mapSource{1: {{Track: 0, T: 10}}, 2: {{Track: 1, T: 50}, {Track: 1, T: 60}}}
	var m Matcher
	first, _ := m.Search(src, []fingerprint.Landmark{{Hash: 1, T: 0}}, 5)
	second, _ := m.Search(src, []fingerprint.Landmark{{Hash: 2, T: 0}, {Hash: 2, T: 10}}, 5)
	// first must not be overwritten by the second search.
	if !slices.Equal(first.Candidates, []Candidate{{Track: 0, Score: 1, Offset: 10}}) {
		t.Errorf("first result changed: %v", first.Candidates)
	}
	if second.Candidates[0] != (Candidate{Track: 1, Score: 2, Offset: 50}) || second.PostingsScanned != 4 {
		t.Errorf("second = %+v", second)
	}
}

func TestSearchEmpty(t *testing.T) {
	var m Matcher
	res, err := m.Search(mapSource{}, []fingerprint.Landmark{{Hash: 5}}, 10)
	if err != nil || len(res.Candidates) != 0 || res.PostingsScanned != 0 {
		t.Fatalf("got %+v, %v", res, err)
	}
}

type goldenMatch struct {
	Tracks  []string `json:"tracks"`
	TopK    int      `json:"top_k"`
	Queries []struct {
		TrueTrack       int        `json:"true_track"`
		PostingsScanned uint64     `json:"postings_scanned"`
		Candidates      [][3]int64 `json:"candidates"`
	} `json:"queries"`
}

type goldenSet struct {
	Hashes []uint32 `json:"hashes"`
	Times  []uint32 `json:"times"`
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// TestParityWithPython rebuilds the prototype's golden index as a real
// segment and checks every query's ranking against prototype/match.py.
func TestParityWithPython(t *testing.T) {
	var g goldenMatch
	readJSON(t, filepath.Join(goldenDir, "match.json"), &g)
	sets := make([]goldenSet, len(g.Tracks))
	var entries []segment.Entry
	for i, name := range g.Tracks {
		readJSON(t, filepath.Join(goldenDir, name), &sets[i])
		for j := range sets[i].Hashes {
			entries = append(entries, segment.Entry{Hash: sets[i].Hashes[j], Track: uint32(i), T: sets[i].Times[j]})
		}
	}
	path := filepath.Join(t.TempDir(), "golden.lmrk")
	meta := segment.Meta{NPartitions: 1, NTracks: uint32(len(sets)), Encoding: segment.EncodingVarint}
	if err := segment.Write(path, meta, entries); err != nil {
		t.Fatal(err)
	}
	seg, err := segment.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = seg.Close() }()

	var m Matcher
	for i, q := range g.Queries {
		query := goldenQuery(sets, i)
		res, err := m.Search(seg, query, g.TopK)
		if err != nil {
			t.Fatal(err)
		}
		want := make([]Candidate, len(q.Candidates))
		for k, c := range q.Candidates {
			want[k] = Candidate{Track: uint32(c[0]), Score: uint32(c[1]), Offset: int32(c[2])}
		}
		if !slices.Equal(res.Candidates, want) || res.PostingsScanned != q.PostingsScanned {
			t.Errorf("query %d:\n got %v (scanned %d)\nwant %v (scanned %d)",
				i, res.Candidates, res.PostingsScanned, want, q.PostingsScanned)
		}
	}
}

// goldenQuery mirrors prototype.golden_match.make_query.
func goldenQuery(sets []goldenSet, i int) []fingerprint.Landmark {
	var q []fingerprint.Landmark
	for j, tt := range sets[i].Times {
		if tt >= 100 && tt < 530 {
			q = append(q, fingerprint.Landmark{Hash: sets[i].Hashes[j], T: tt - 100})
		}
	}
	next := sets[(i+1)%len(sets)]
	for j, tt := range next.Times {
		if tt < 430 {
			q = append(q, fingerprint.Landmark{Hash: next.Hashes[j], T: tt})
		}
	}
	return q
}

func TestRunnerUpAndAccept(t *testing.T) {
	cluster := func(tr uint32) uint32 {
		if tr == 1 {
			return 0 // track 1 duplicates track 0
		}
		return tr
	}
	tests := []struct {
		name       string
		cands      []Candidate
		wantRunner uint32
		wantAccept bool // at tAbs 10, tRatio 2
	}{
		{"empty", nil, 0, false},
		{"lone winner", []Candidate{{Track: 5, Score: 40}}, 0, true},
		{"close runner-up", []Candidate{{Track: 5, Score: 40}, {Track: 6, Score: 30}}, 30, false},
		{"duplicate is skipped", []Candidate{{Track: 0, Score: 40}, {Track: 1, Score: 40}, {Track: 2, Score: 5}}, 5, true},
		{"below t_abs", []Candidate{{Track: 5, Score: 8}}, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := RunnerUp(tc.cands, cluster)
			if r != tc.wantRunner {
				t.Fatalf("RunnerUp = %d, want %d", r, tc.wantRunner)
			}
			var top uint32
			if len(tc.cands) > 0 {
				top = tc.cands[0].Score
			}
			if got := Accept(top, r, 10, 2); got != tc.wantAccept {
				t.Errorf("Accept = %v, want %v", got, tc.wantAccept)
			}
		})
	}
}
