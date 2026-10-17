// Command postings measures how much index data one query touches as the
// catalog grows, to ground the hash-space decision (B4 at medium) in data.
//
// Every query landmark turns each posting under its hash into one vote, so
// postings scanned per query is the matcher's main cost (it is the size of
// the vote array that gets sorted). This tool simulates smaller catalogs by
// keeping a seeded random subset of the indexed tracks (nested: each
// fraction's subset contains the smaller ones), and reports for each size:
// keys in use, mean posting-list length, and postings scanned per query.
// At the full catalog it also reports how concentrated the scanned postings
// are in the longest lists, and what capping list length would remove.
package main

import (
	"encoding/gob"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/runinfo"
	"github.com/anytroops/landmark/internal/segment"
)

type query struct {
	FMAID     int
	Expected  int32
	Landmarks []fingerprint.Landmark
}

// reachableKeys is how many hashes D10's pairing can produce: f1 has 512
// values, f2 is within +/-64 bins of f1, and 1 <= dt <= 63.
const reachableKeys = 512 * 129 * 63

type sizeRow struct {
	Fraction        float64 `json:"fraction"`
	Tracks          int     `json:"tracks"`
	Postings        uint64  `json:"postings"`
	KeysUsed        int     `json:"keys_used"`
	ReachableUsed   float64 `json:"reachable_keys_used"`
	MeanListLen     float64 `json:"mean_list_len"`
	ScannedMean     float64 `json:"scanned_per_query_mean"`
	ScannedP50      uint64  `json:"scanned_per_query_p50"`
	ScannedP99      uint64  `json:"scanned_per_query_p99"`
	ScannedPerTrack float64 `json:"scanned_per_query_per_1k_tracks"`
}

type capRow struct {
	MaxListLen    int     `json:"max_list_len"`
	KeysDropped   float64 `json:"keys_dropped"`
	LandmarksKept float64 `json:"query_landmarks_kept"`
	ScannedKept   float64 `json:"scanned_kept"`
}

type skewRow struct {
	TopKeys       float64 `json:"top_keys_by_list_len"`
	MinListLen    int     `json:"min_list_len"`
	ShareOfScan   float64 `json:"share_of_scanned"`
	ShareOfLookup float64 `json:"share_of_query_landmarks"`
}

func main() {
	segPath := flag.String("segment", "data/segments/medium/p1/part-000.lmrk", "single-partition segment")
	qPath := flag.String("queries", "data/queries/medium_test_10s_seed0.gob", "loadgen query file")
	seed := flag.Uint64("seed", 0, "track subset seed")
	out := flag.String("out", "bench/results/postings_medium.json", "output JSON")
	flag.Parse()
	if err := run(*segPath, *qPath, *seed, *out); err != nil {
		log.Fatal(err)
	}
}

func run(segPath, qPath string, seed uint64, out string) error {
	s, err := segment.Open(segPath)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	if _, n := s.Partition(); n != 1 {
		return fmt.Errorf("%s is one of %d partitions; use a P=1 index", segPath, n)
	}
	qs, err := loadQueries(qPath)
	if err != nil {
		return err
	}

	fractions := []float64{1.0 / 16, 1.0 / 8, 1.0 / 4, 1.0 / 2, 1}
	nTracks := int(s.NTracks())
	// rank[t] < limit[i] means track t is in fraction i's catalog.
	rank := rand.New(rand.NewPCG(seed, 0)).Perm(nTracks)
	limit := make([]int, len(fractions))
	for i, f := range fractions {
		limit[i] = int(f * float64(nTracks))
	}

	// counts[i*nKeys+k] = postings under key k from fraction i's tracks.
	nKeys := s.NKeys()
	keys := make([]uint32, nKeys)
	counts := make([]uint32, len(fractions)*nKeys)
	var buf []segment.Posting
	for k := range nKeys {
		keys[k] = s.Key(k)
		if buf, err = s.AppendPostings(buf[:0], keys[k]); err != nil {
			return fmt.Errorf("key %d: %w", keys[k], err)
		}
		for _, p := range buf {
			r := rank[p.Track]
			for i := range fractions {
				if r < limit[i] {
					counts[i*nKeys+k]++
				}
			}
		}
	}
	find := func(h uint32) (int, bool) {
		k := sort.Search(nKeys, func(i int) bool { return keys[i] >= h })
		return k, k < nKeys && keys[k] == h
	}

	var res struct {
		Segment    string            `json:"segment"`
		Queries    string            `json:"queries"`
		NQueries   int               `json:"n_queries"`
		LandmarksQ float64           `json:"landmarks_per_query_mean"`
		Reachable  int               `json:"reachable_keys"`
		Seed       uint64            `json:"seed"`
		Sizes      []sizeRow         `json:"sizes"`
		Skew       []skewRow         `json:"skew_full_catalog"`
		Caps       []capRow          `json:"caps_full_catalog"`
		Machine    map[string]string `json:"machine"`
		Note       string            `json:"note"`
	}
	res.Segment, res.Queries, res.NQueries, res.Reachable, res.Seed = segPath, qPath, len(qs), reachableKeys, seed
	res.Machine = runinfo.Machine(".")
	res.Note = "scanned = sum over query landmarks of posting-list length under the subset catalog, i.e. votes the matcher sorts"

	var landmarks int
	for _, q := range qs {
		landmarks += len(q.Landmarks)
	}
	res.LandmarksQ = float64(landmarks) / float64(len(qs))

	for i, f := range fractions {
		row := sizeRow{Fraction: f, Tracks: limit[i]}
		c := counts[i*nKeys : (i+1)*nKeys]
		for _, n := range c {
			row.Postings += uint64(n)
			if n > 0 {
				row.KeysUsed++
			}
		}
		row.ReachableUsed = float64(row.KeysUsed) / reachableKeys
		row.MeanListLen = float64(row.Postings) / float64(row.KeysUsed)
		scanned := make([]uint64, len(qs))
		var total uint64
		for j, q := range qs {
			for _, l := range q.Landmarks {
				if k, ok := find(l.Hash); ok {
					scanned[j] += uint64(c[k])
				}
			}
			total += scanned[j]
		}
		slices.Sort(scanned)
		row.ScannedMean = float64(total) / float64(len(qs))
		row.ScannedP50 = scanned[len(scanned)/2]
		row.ScannedP99 = scanned[len(scanned)*99/100]
		row.ScannedPerTrack = row.ScannedMean / float64(row.Tracks) * 1000
		res.Sizes = append(res.Sizes, row)
	}

	// Skew and caps at the full catalog.
	full := counts[(len(fractions)-1)*nKeys:]
	byLen := slices.Clone(full)
	slices.Sort(byLen)
	var hitLens []uint32 // list length behind every query landmark that hits a key
	var totalScan uint64
	for _, q := range qs {
		for _, l := range q.Landmarks {
			if k, ok := find(l.Hash); ok {
				hitLens = append(hitLens, full[k])
				totalScan += uint64(full[k])
			}
		}
	}
	for _, top := range []float64{0.001, 0.01, 0.05, 0.10} {
		minLen := byLen[int(float64(nKeys)*(1-top))]
		row := skewRow{TopKeys: top, MinListLen: int(minLen)}
		var scan, hits uint64
		for _, n := range hitLens {
			if n >= minLen {
				scan += uint64(n)
				hits++
			}
		}
		row.ShareOfScan = float64(scan) / float64(totalScan)
		row.ShareOfLookup = float64(hits) / float64(landmarks)
		res.Skew = append(res.Skew, row)
	}
	for _, maxLen := range []int{64, 128, 256, 512, 1024} {
		row := capRow{MaxListLen: maxLen}
		var dropped int
		for _, n := range full {
			if int(n) > maxLen {
				dropped++
			}
		}
		row.KeysDropped = float64(dropped) / float64(nKeys)
		var scan, hits uint64
		for _, n := range hitLens {
			if int(n) <= maxLen {
				scan += uint64(n)
				hits++
			}
		}
		row.LandmarksKept = float64(hits) / float64(landmarks)
		row.ScannedKept = float64(scan) / float64(totalScan)
		res.Caps = append(res.Caps, row)
	}

	for _, r := range res.Sizes {
		fmt.Printf("tracks=%6d keys=%8d (%.0f%% of reachable) mean_list=%6.1f scanned/query mean=%9.0f p50=%8d p99=%8d per_1k_tracks=%.0f\n",
			r.Tracks, r.KeysUsed, 100*r.ReachableUsed, r.MeanListLen, r.ScannedMean, r.ScannedP50, r.ScannedP99, r.ScannedPerTrack)
	}
	for _, r := range res.Skew {
		fmt.Printf("top %.1f%% of keys (len >= %d): %.1f%% of scanned, %.1f%% of query landmarks\n",
			100*r.TopKeys, r.MinListLen, 100*r.ShareOfScan, 100*r.ShareOfLookup)
	}
	for _, r := range res.Caps {
		fmt.Printf("cap %4d: drops %.2f%% of keys, keeps %.1f%% of query landmarks and %.1f%% of scanned\n",
			r.MaxListLen, 100*r.KeysDropped, 100*r.LandmarksKept, 100*r.ScannedKept)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(res, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(out, append(b, '\n'), 0o644)
}

func loadQueries(path string) ([]query, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var qs []query
	if err := gob.NewDecoder(f).Decode(&qs); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if len(qs) == 0 {
		return nil, fmt.Errorf("%s has no queries", path)
	}
	return qs, nil
}
