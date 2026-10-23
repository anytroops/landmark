// Command querycost measures client-side fingerprinting cost per DJ-mix
// window and per indexed track, for the pair fingerprint and the B2 triplet
// fingerprint (D31, D33, D35), on one core, and the hybrid's whole
// per-window fingerprinting (D36: triplets as is, at double and half time,
// and pairs with their keylock rescales).
//
// Per window (a 10 s excerpt, 4 sub-hop shifts): the pair query
// fingerprint, which the speed grid repeats once per audio-undone
// hypothesis (17 for +/-8% in 1% turntable steps plus the unchanged
// window; keylock hypotheses rescale landmarks instead), and the B2 dual
// query fingerprint, done once. Resampling for the grid is not timed, so
// the grid figure is a lower bound. Per track: the index-side fingerprint
// of a whole clip, which sets ingest cost.
//
// Tracks are fma_medium test positives, decoded up front; excerpts start at
// the same seeded offsets as cmd/loadgen queries.
//
//	go run ./bench/querycost -n 100 -out bench/results/b5_querycost.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/runinfo"
	"github.com/anytroops/landmark/internal/triplet"
)

const gridAudioHypotheses = 17 // the unchanged window plus 16 turntable steps

type timing struct {
	MedianMs float64 `json:"median_ms"`
	P90Ms    float64 `json:"p90_ms"`
	MeanMs   float64 `json:"mean_ms"`
}

type result struct {
	Tracks              int               `json:"tracks"`
	WindowSeconds       float64           `json:"window_seconds"`
	Shifts              int               `json:"shifts"`
	Reps                int               `json:"reps"`
	PairQuery           timing            `json:"pair_query_per_window"`
	GridQueryLowerBound timing            `json:"grid_query_per_window_lower_bound"`
	B2DualQuery         timing            `json:"b2_dual_query_per_window"`
	// The hybrid's other per-window fingerprints (D36): the window resampled
	// to double and half time (resampling included) for the triplet
	// searches, and its whole client-side work per window.
	B2DoubleTime timing `json:"b2_double_time_per_window"`
	B2HalfTime   timing `json:"b2_half_time_per_window"`
	HybridWindow timing `json:"hybrid_fingerprints_per_window"`
	PairIndex           timing            `json:"pair_index_per_track"`
	B2Index             timing            `json:"b2_index_per_track"`
	PairQueryLandmarks  float64           `json:"pair_query_landmarks_median"`
	B2QueryLandmarks    float64           `json:"b2_query_landmarks_median"`
	GridAudioHyps       int               `json:"grid_audio_hypotheses"`
	Machine             map[string]string `json:"machine"`
}

func main() {
	root := flag.String("root", ".", "repo root")
	n := flag.Int("n", 100, "first N test positives of the fma_medium manifest")
	reps := flag.Int("reps", 3, "passes over the tracks; each sample is one track's best pass")
	seconds := flag.Float64("seconds", 10, "window length")
	shifts := flag.Int("shifts", 4, "sub-hop query grids (D15)")
	seed := flag.Uint64("seed", 0, "excerpt offset seed (as cmd/loadgen)")
	out := flag.String("out", "", "output JSON path (required)")
	flag.Parse()
	if *out == "" {
		log.Fatal("-out is required")
	}

	ids, tracks := loadTracks(*root, *n)
	w := int(*seconds * audio.SampleRate)
	var windows [][]float32
	for i, pcm := range tracks {
		if len(pcm) < w {
			continue
		}
		rng := rand.New(rand.NewPCG(*seed, uint64(ids[i])))
		start := rng.IntN(len(pcm) - w + 1)
		windows = append(windows, pcm[start:start+w])
	}
	log.Printf("%d tracks, %d windows", len(tracks), len(windows))

	pairs := fingerprint.New(fingerprint.DefaultParams())
	trips := triplet.New(triplet.DefaultParams())
	res := result{Tracks: len(tracks), WindowSeconds: *seconds, Shifts: *shifts, Reps: *reps,
		GridAudioHyps: gridAudioHypotheses, Machine: runinfo.Machine(*root)}

	var pairN, tripN []float64
	pairQ := best(*reps, windows, func(x []float32) { pairN = append(pairN, float64(len(pairs.FingerprintQuery(x, *shifts)))) })
	b2Q := best(*reps, windows, func(x []float32) { tripN = append(tripN, float64(len(trips.FingerprintQuery(x, *shifts)))) })
	res.PairQuery, res.B2DualQuery = summarize(pairQ), summarize(b2Q)
	grid := make([]float64, len(pairQ))
	for i, v := range pairQ {
		grid[i] = gridAudioHypotheses * v
	}
	res.GridQueryLowerBound = summarize(grid)
	res.B2DoubleTime = summarize(best(*reps, windows, func(x []float32) { trips.FingerprintQuery(audio.ResamplePoly(x, 2, 1), *shifts) }))
	res.B2HalfTime = summarize(best(*reps, windows, func(x []float32) { trips.FingerprintQuery(audio.ResamplePoly(x, 1, 2), *shifts) }))
	res.HybridWindow = summarize(best(*reps, windows, func(x []float32) {
		trips.FingerprintQuery(x, *shifts)
		trips.FingerprintQuery(audio.ResamplePoly(x, 2, 1), *shifts)
		trips.FingerprintQuery(audio.ResamplePoly(x, 1, 2), *shifts)
		lms := pairs.FingerprintQuery(x, *shifts)
		fingerprint.RescaleTempo(lms, 2)
		fingerprint.RescaleTempo(lms, 0.5)
	}))
	res.PairQueryLandmarks, res.B2QueryLandmarks = median(pairN), median(tripN)
	res.PairIndex = summarize(best(*reps, tracks, func(x []float32) { pairs.Fingerprint(x) }))
	res.B2Index = summarize(best(*reps, tracks, func(x []float32) { trips.Fingerprint(x) }))

	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("per window: pairs %.1f ms (grid >= %.0f ms), B2 dual %.1f ms, double time %.1f ms, half time %.1f ms, hybrid %.1f ms; per track: pairs %.1f ms, B2 %.1f ms",
		res.PairQuery.MedianMs, res.GridQueryLowerBound.MedianMs, res.B2DualQuery.MedianMs, res.B2DoubleTime.MedianMs,
		res.B2HalfTime.MedianMs, res.HybridWindow.MedianMs, res.PairIndex.MedianMs, res.B2Index.MedianMs)
}

// best runs f over every input reps times and returns each input's fastest
// time in milliseconds (the least disturbed by other work on the machine).
func best(reps int, inputs [][]float32, f func([]float32)) []float64 {
	out := make([]float64, len(inputs))
	for r := range reps {
		for i, x := range inputs {
			start := time.Now()
			f(x)
			ms := float64(time.Since(start).Microseconds()) / 1000
			if r == 0 || ms < out[i] {
				out[i] = ms
			}
		}
	}
	return out
}

func summarize(ms []float64) timing {
	s := slices.Sorted(slices.Values(ms))
	sum := 0.0
	for _, v := range s {
		sum += v
	}
	return timing{MedianMs: median(s), P90Ms: s[int(0.9*float64(len(s)-1))], MeanMs: sum / float64(len(s))}
}

func median(v []float64) float64 {
	s := slices.Sorted(slices.Values(v))
	if len(s) == 0 {
		return 0
	}
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// loadTracks decodes the first n test positives (by FMA id) and returns the
// ids and clips of those that decode.
func loadTracks(root string, n int) ([]int, [][]float32) {
	b, err := os.ReadFile(filepath.Join(root, "eval/manifests/splits_medium_seed0.json"))
	if err != nil {
		log.Fatal(err)
	}
	var m struct {
		TestPos []int `json:"test_pos"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		log.Fatal(err)
	}
	ids := slices.Clone(m.TestPos)
	sort.Ints(ids)
	var kept []int
	var pcms [][]float32
	for _, id := range ids[:min(n, len(ids))] {
		s := fmt.Sprintf("%06d", id)
		pcm, err := audio.Decode(context.Background(),
			filepath.Join(root, "data/fma_medium", s[:3], s+".mp3"), audio.SampleRate)
		if err != nil {
			log.Printf("skip %d: %v", id, err)
			continue
		}
		kept, pcms = append(kept, id), append(pcms, pcm)
	}
	return kept, pcms
}
