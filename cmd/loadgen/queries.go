package main

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"

	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/fingerprint"
)

// Query is one pre-fingerprinted request. Expected is the dense track id a
// correct answer names, or -1 for a never-indexed song.
type Query struct {
	FMAID     int
	Expected  int32
	Landmarks []fingerprint.Landmark
}

func queriesCmd(args []string) error {
	fs := flag.NewFlagSet("queries", flag.ExitOnError)
	manifestPath := fs.String("manifest", "eval/manifests/splits_small_seed0.json", "split manifest")
	data := fs.String("data", "data", "dataset root")
	split := fs.String("split", "test", "split whose pos/neg tracks become queries")
	seconds := fs.Float64("seconds", 10, "excerpt length")
	shifts := fs.Int("shifts", 4, "sub-hop query grids (D15)")
	seed := fs.Uint64("seed", 0, "excerpt offset seed")
	out := fs.String("out", "", "output .gob path (required)")
	_ = fs.Parse(args)
	if *out == "" {
		return errors.New("-out is required")
	}

	b, err := os.ReadFile(*manifestPath)
	if err != nil {
		return err
	}
	var m struct {
		Meta struct {
			Subset string `json:"subset"`
		} `json:"meta"`
		Indexed []int `json:"indexed"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	var raw map[string]json.RawMessage // for the split-named lists
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	var pos, neg []int
	if err := json.Unmarshal(raw[*split+"_pos"], &pos); err != nil {
		return fmt.Errorf("%s_pos: %w", *split, err)
	}
	if err := json.Unmarshal(raw[*split+"_neg"], &neg); err != nil {
		return fmt.Errorf("%s_neg: %w", *split, err)
	}
	indexed := slices.Sorted(slices.Values(m.Indexed))
	dense := make(map[int]int32, len(indexed))
	for i, id := range indexed {
		dense[id] = int32(i)
	}

	type job struct {
		fma      int
		expected int32
	}
	var jobs []job
	for _, id := range pos {
		jobs = append(jobs, job{id, dense[id]})
	}
	for _, id := range neg {
		jobs = append(jobs, job{id, -1})
	}

	queries := make([]*Query, len(jobs))
	work := make(chan int)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Go(func() {
			fp := fingerprint.New(fingerprint.DefaultParams())
			for i := range work {
				j := jobs[i]
				s := fmt.Sprintf("%06d", j.fma)
				pcm, err := audio.Decode(context.Background(),
					filepath.Join(*data, "fma_"+m.Meta.Subset, s[:3], s+".mp3"), audio.SampleRate)
				n := int(*seconds * audio.SampleRate)
				if err != nil || len(pcm) < n {
					slog.Warn("skip", "fma_id", j.fma, "err", err, "samples", len(pcm))
					continue
				}
				rng := rand.New(rand.NewPCG(*seed, uint64(j.fma)))
				start := rng.IntN(len(pcm) - n + 1)
				queries[i] = &Query{FMAID: j.fma, Expected: j.expected,
					Landmarks: fp.FingerprintQuery(pcm[start:start+n], *shifts)}
			}
		})
	}
	for i := range jobs {
		work <- i
	}
	close(work)
	wg.Wait()

	var kept []Query
	for _, q := range queries {
		if q != nil {
			kept = append(kept, *q)
		}
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(f).Encode(kept); err != nil {
		_ = f.Close()
		return err
	}
	slog.Info("wrote queries", "path", *out, "queries", len(kept), "of", len(jobs))
	return f.Close()
}

func loadQueries(path string) ([]Query, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var qs []Query
	if err := gob.NewDecoder(f).Decode(&qs); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if len(qs) == 0 {
		return nil, fmt.Errorf("%s has no queries", path)
	}
	return qs, nil
}
