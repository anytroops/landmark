// Command frontend measures option T1 (a) of docs/design/throughput.md
// before anything is built: a stream's triplet spectrograms computed once,
// on a 64-sample grid, as audio arrives, and each window's spectrograms
// (4 sub-hop grids, both filterbanks, three speeds) cut out of them, with
// the window hop a multiple of 128 samples (27,520) so every window's grids
// fall on the stream's. Peak picking and hashing stay per window.
//
// The stream is the first n fma_medium test positives back to back, like a
// station playing them. It reports, per window on one core: today's
// triplet fingerprinting (the window as is, at double and at half time,
// each resampled per window, D35 and D44), and the shared front end's cost
// per hop (its spectrogram and resampling work divided by the hops, plus
// one window's peaks and hashes), which is the steady-state cost of a
// window. It also checks that the cut-out windows give exactly today's
// landmarks at the window's own speed, and reports how many landmarks
// change at double and half time, where the stream rather than each window
// is resampled (the window's resampling filter sees its edges). Pair
// fingerprinting (32 ms, B13) is the same in both and not timed.
//
//	go run ./bench/frontend -n 10 -out bench/results/b14_frontend.json
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
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

const (
	windowSamples = 10 * audio.SampleRate // 110,250
	todayHop      = 27562                 // int(2.5 * SampleRate), cmd/monitor's
	alignedHop    = 27520                 // 215 x 128: the multiple of 128 nearest 2.5 s
	grid          = 64                    // sub-hop step (D15: 256 / 4 shifts)
	shifts        = 4
)

type timing struct {
	MedianMs float64 `json:"median_ms"`
	MeanMs   float64 `json:"mean_ms"`
}

type speedResult struct {
	Speed string `json:"speed"`
	// Windows compared and their landmarks: today's (resampled per window)
	// and the shared front end's (cut from the resampled stream).
	Windows         int     `json:"windows"`
	TodayLandmarks  int     `json:"today_landmarks"`
	SharedLandmarks int     `json:"shared_landmarks"`
	Jaccard         float64 `json:"jaccard"`
	IdenticalWins   int     `json:"identical_windows"`
}

type result struct {
	Tracks        int     `json:"tracks"`
	StreamSeconds float64 `json:"stream_seconds"`
	Reps          int     `json:"reps"`
	// Today, per window: the three triplet query fingerprints, resampling
	// included.
	TodayPerWindow timing `json:"today_triplets_per_window"`
	// Shared front end: the stream's spectrograms at three speeds and its
	// resampling, per hop; one window's peaks and hashes; their sum.
	SharedPerHopMs    float64           `json:"shared_spectrograms_per_hop_ms"`
	ResamplePerHopMs  float64           `json:"stream_resampling_per_hop_ms"`
	CutPerWindow      timing            `json:"peaks_and_hashes_per_window"`
	SharedPerWindowMs float64           `json:"shared_total_per_window_ms"`
	Speeds            []speedResult     `json:"speeds"`
	Machine           map[string]string `json:"machine"`
}

func main() {
	root := flag.String("root", ".", "repo root")
	n := flag.Int("n", 10, "first N test positives of the fma_medium manifest, back to back")
	reps := flag.Int("reps", 3, "passes; each figure is the fastest pass")
	out := flag.String("out", "", "output JSON path (required)")
	flag.Parse()
	if *out == "" {
		log.Fatal("-out is required")
	}
	stream := loadStream(*root, *n)
	log.Printf("stream of %.0f s", float64(len(stream))/audio.SampleRate)
	res := result{Tracks: *n, StreamSeconds: float64(len(stream)) / audio.SampleRate, Reps: *reps,
		Machine: runinfo.Machine(*root)}

	p := triplet.DefaultParams()
	fp := triplet.New(p)
	speeds := []struct {
		name string
		up   int // resample by up/down
		down int
	}{{"as is", 1, 1}, {"double time", 2, 1}, {"half time", 1, 2}}

	// Today: windows every todayHop, each fingerprinted at three speeds.
	var today []float64
	for r := range *reps {
		i := 0
		for s := 0; s+windowSamples <= len(stream); s += todayHop {
			x := stream[s : s+windowSamples]
			start := time.Now()
			fp.FingerprintQuery(x, shifts)
			fp.FingerprintQuery(audio.ResamplePoly(x, 2, 1), shifts)
			fp.FingerprintQuery(audio.ResamplePoly(x, 1, 2), shifts)
			ms := msSince(start)
			if r == 0 {
				today = append(today, ms)
			} else {
				today[i] = min(today[i], ms)
			}
			i++
		}
	}
	res.TodayPerWindow = summarize(today)

	// Shared: the stream resampled once per speed, its spectrograms on the
	// 64-sample grid once, then each window cut out of them.
	fine := p
	fine.Hop = grid
	sfp := triplet.New(fine)
	hops := 0
	for s := 0; s+windowSamples <= len(stream); s += alignedHop {
		hops++
	}
	var resampleMs, specMs float64
	streams := make([][]float32, len(speeds))
	specs := make([][2]fingerprint.Spectrogram, len(speeds))
	for r := range *reps {
		var rs, sp float64
		for i, sd := range speeds {
			start := time.Now()
			streams[i] = stream
			if sd.up != sd.down {
				streams[i] = audio.ResamplePoly(stream, sd.up, sd.down)
			}
			rs += msSince(start)
			start = time.Now()
			specs[i] = sfp.QuerySpectrograms(streams[i])
			sp += msSince(start)
		}
		if r == 0 || rs+sp < resampleMs+specMs {
			resampleMs, specMs = rs, sp
		}
	}
	res.ResamplePerHopMs = resampleMs / float64(hops)
	res.SharedPerHopMs = specMs / float64(hops)

	var cut []float64
	res.Speeds = make([]speedResult, len(speeds))
	for i, sd := range speeds {
		res.Speeds[i].Speed = sd.name
	}
	inter, union := make([]int, len(speeds)), make([]int, len(speeds))
	for r := range *reps {
		w := 0
		for s := 0; s+windowSamples <= len(stream); s += alignedHop {
			start := time.Now()
			var got [3][]triplet.Landmark
			for i, sd := range speeds {
				got[i] = cutQuery(specs[i], s*sd.up/sd.down, windowSamples*sd.up/sd.down, p)
			}
			ms := msSince(start)
			if r == 0 {
				cut = append(cut, ms)
			} else {
				cut[w] = min(cut[w], ms)
			}
			w++
			if r > 0 {
				continue
			}
			x := stream[s : s+windowSamples]
			for i, sd := range speeds {
				y := x
				if sd.up != sd.down {
					y = audio.ResamplePoly(x, sd.up, sd.down)
				}
				want := fp.FingerprintQuery(y, shifts)
				sr := &res.Speeds[i]
				sr.Windows++
				sr.TodayLandmarks += len(want)
				sr.SharedLandmarks += len(got[i])
				if slices.Equal(got[i], want) {
					sr.IdenticalWins++
				}
				a, b := overlap(got[i], want)
				inter[i] += a
				union[i] += b
			}
		}
	}
	for i := range res.Speeds {
		res.Speeds[i].Jaccard = float64(inter[i]) / float64(union[i])
	}
	if s := res.Speeds[0]; s.IdenticalWins != s.Windows {
		log.Fatalf("at the window's own speed %d of %d windows differ from FingerprintQuery: the cut is wrong",
			s.Windows-s.IdenticalWins, s.Windows)
	}
	res.CutPerWindow = summarize(cut)
	res.SharedPerWindowMs = res.SharedPerHopMs + res.ResamplePerHopMs + res.CutPerWindow.MeanMs

	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("per window: today %.1f ms; shared %.1f ms (spectrograms %.1f + resampling %.1f per hop, peaks and hashes %.1f)",
		res.TodayPerWindow.MeanMs, res.SharedPerWindowMs, res.SharedPerHopMs, res.ResamplePerHopMs, res.CutPerWindow.MeanMs)
	for _, s := range res.Speeds {
		log.Printf("%s: %d/%d windows identical, Jaccard %.4f", s.Speed, s.IdenticalWins, s.Windows, s.Jaccard)
	}
}

// cutQuery is FingerprintQuery of the window of length samples starting at
// sample start of the stream whose 64-sample-grid spectrograms are specs:
// each shift's frames are every fourth grid frame from (start + 64k) / 64.
func cutQuery(specs [2]fingerprint.Spectrogram, start, length int, p triplet.Params) []triplet.Landmark {
	step := p.Hop / grid
	var all []triplet.Landmark
	for k := range shifts {
		frames := (length-k*grid-p.NFFT)/p.Hop + 1
		if frames <= 0 {
			continue
		}
		first := (start + k*grid) / grid
		for _, s := range specs {
			w := fingerprint.Spectrogram{Frames: frames, Bins: s.Bins, Data: make([]float64, frames*s.Bins)}
			for t := range frames {
				src := (first + t*step) * s.Bins
				copy(w.Data[t*s.Bins:(t+1)*s.Bins], s.Data[src:src+s.Bins])
			}
			all = append(all, triplet.Triplets(fingerprint.FindPeaks(w, p.PeakParams()), p)...)
		}
	}
	slices.SortFunc(all, func(a, b triplet.Landmark) int {
		return cmp.Or(cmp.Compare(a.Hash, b.Hash), cmp.Compare(a.T, b.T), cmp.Compare(a.Span, b.Span), cmp.Compare(a.Band, b.Band))
	})
	return slices.Compact(all)
}

// overlap returns the sizes of the intersection and union of two sorted,
// duplicate-free landmark lists.
func overlap(a, b []triplet.Landmark) (int, int) {
	in := map[triplet.Landmark]bool{}
	for _, l := range a {
		in[l] = true
	}
	both := 0
	for _, l := range b {
		if in[l] {
			both++
		}
	}
	return both, len(a) + len(b) - both
}

func msSince(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000 }

func summarize(ms []float64) timing {
	s := slices.Sorted(slices.Values(ms))
	sum := 0.0
	for _, v := range s {
		sum += v
	}
	med := s[len(s)/2]
	if len(s)%2 == 0 {
		med = (s[len(s)/2-1] + s[len(s)/2]) / 2
	}
	return timing{MedianMs: med, MeanMs: sum / float64(len(s))}
}

// loadStream decodes the first n test positives (by FMA id) and plays them
// back to back.
func loadStream(root string, n int) []float32 {
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
	var stream []float32
	for _, id := range ids[:min(n, len(ids))] {
		s := fmt.Sprintf("%06d", id)
		pcm, err := audio.Decode(context.Background(), filepath.Join(root, "data/fma_medium", s[:3], s+".mp3"), audio.SampleRate)
		if err != nil {
			log.Printf("skip %d: %v", id, err)
			continue
		}
		stream = append(stream, pcm...)
	}
	return stream
}
