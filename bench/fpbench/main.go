// Command fpbench measures Go fingerprinting throughput on real tracks.
//
// Tracks are decoded up front, so only fingerprinting is timed. The
// all-cores run splits tracks into equal static chunks, one goroutine each,
// and reports wall time as the slowest worker; bench/scripts/fp_throughput.py
// uses the same method so the Go and Python numbers are comparable.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/runinfo"
)

type result struct {
	Lang          string            `json:"lang"`
	Tracks        int               `json:"tracks"`
	AudioSeconds  float64           `json:"audio_seconds"`
	Landmarks     int               `json:"landmarks"`
	Runs          []run             `json:"runs"`
	Machine       map[string]string `json:"machine"`
	QueryShifts   int               `json:"query_shifts"`
	ManifestFirst int               `json:"manifest_first_n"`
}

type run struct {
	Workers          int     `json:"workers"`
	Rep              int     `json:"rep"`
	WallSeconds      float64 `json:"wall_seconds"`
	AudioSecPerSec   float64 `json:"audio_seconds_per_wall_second"`
	MillisPer30sClip float64 `json:"ms_per_30s_clip_per_worker"`
}

func main() {
	root := flag.String("root", ".", "repo root")
	n := flag.Int("n", 200, "first N indexed tracks from the manifest")
	reps := flag.Int("reps", 3, "repetitions per configuration")
	out := flag.String("out", "bench/results/fp_throughput_go.json", "output path")
	flag.Parse()

	pcms, secs := loadTracks(*root, *n)
	log.Printf("decoded %d tracks, %.0f s of audio", len(pcms), secs)

	res := result{Lang: "go", Tracks: len(pcms), AudioSeconds: secs, ManifestFirst: *n,
		QueryShifts: 1, Machine: runinfo.Machine(*root)}
	res.Landmarks = countLandmarks(pcms)
	for _, workers := range []int{1, runtime.NumCPU()} {
		for rep := 0; rep < *reps; rep++ {
			wall := timeWorkers(pcms, workers)
			r := run{Workers: workers, Rep: rep, WallSeconds: wall, AudioSecPerSec: secs / wall,
				MillisPer30sClip: 1000 * wall * float64(workers) / (secs / 30)}
			log.Printf("workers=%d rep=%d wall=%.2fs throughput=%.0fx realtime", workers, rep, wall, r.AudioSecPerSec)
			res.Runs = append(res.Runs, r)
		}
	}
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*root, *out), append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
}

func loadTracks(root string, n int) ([][]float32, float64) {
	b, err := os.ReadFile(filepath.Join(root, "eval/manifests/splits_small_seed0.json"))
	if err != nil {
		log.Fatal(err)
	}
	var m struct {
		Indexed []int `json:"indexed"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		log.Fatal(err)
	}
	var pcms [][]float32
	secs := 0.0
	for _, id := range m.Indexed[:min(n, len(m.Indexed))] {
		s := fmt.Sprintf("%06d", id)
		pcm, err := audio.Decode(context.Background(),
			filepath.Join(root, "data/fma_small", s[:3], s+".mp3"), audio.SampleRate)
		if err != nil {
			log.Printf("skip %d: %v", id, err)
			continue
		}
		pcms = append(pcms, pcm)
		secs += float64(len(pcm)) / audio.SampleRate
	}
	return pcms, secs
}

func countLandmarks(pcms [][]float32) int {
	fp := fingerprint.New(fingerprint.DefaultParams())
	total := 0
	for _, pcm := range pcms {
		total += len(fp.Fingerprint(pcm))
	}
	return total
}

// timeWorkers fingerprints all tracks split into `workers` equal chunks, one
// goroutine per chunk, and returns the slowest chunk's time in seconds.
func timeWorkers(pcms [][]float32, workers int) float64 {
	elapsed := make([]time.Duration, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fp := fingerprint.New(fingerprint.DefaultParams()) // not shared: has scratch buffers
			start := time.Now()
			for i := w; i < len(pcms); i += workers {
				fp.Fingerprint(pcms[i])
			}
			elapsed[w] = time.Since(start) // each goroutine writes only its own slot
		}()
	}
	wg.Wait()
	slowest := time.Duration(0)
	for _, d := range elapsed {
		slowest = max(slowest, d)
	}
	return slowest.Seconds()
}
