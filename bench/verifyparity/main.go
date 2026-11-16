// Command verifyparity checks internal/analysis.Verify (the Go audio check
// of a tracklist entry, D57) against eval/verify_set.py's results on one
// set: for each entry it recomputes the correlation at the prototype's
// alignment, which must agree closely (the same arithmetic on the same
// spectrograms), and the Z against its own random nulls, which agrees in
// distribution only, so the pass or fail at analysis.VerifyMark is what is
// compared there.
//
//	go run ./bench/verifyparity -verify data/mine/pitch.verify.json \
//	    -audio set.wav -index data/segments/mine_b2
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"os"

	"github.com/anytroops/landmark/internal/analysis"
	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/catalog"
	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/triplet"
)

type pyEntry struct {
	Track int      `json:"track"`
	Name  string   `json:"name"`
	Start float64  `json:"start_s"`
	End   float64  `json:"end_s"`
	Corr  *float64 `json:"corr"`
	Z     *float64 `json:"z"`
	A     *float64 `json:"a"`
	Rate  *float64 `json:"rate"`
	Pitch *bool    `json:"pitch"`
}

func main() {
	verifyPath := flag.String("verify", "", "eval.verify_set output for the set (required)")
	audioPath := flag.String("audio", "", "the set's audio (required)")
	index := flag.String("index", "", "the index the set was searched in (required)")
	poolSize := flag.Int("pool", 60, "catalog tracks drawn for the nulls")
	flag.Parse()
	if *verifyPath == "" || *audioPath == "" || *index == "" {
		log.Fatal("-verify, -audio, and -index are required")
	}
	if err := run(*verifyPath, *audioPath, *index, *poolSize); err != nil {
		log.Fatal(err)
	}
}

func run(verifyPath, audioPath, index string, poolSize int) error {
	b, err := os.ReadFile(verifyPath)
	if err != nil {
		return err
	}
	var entries []pyEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return err
	}
	cat, err := catalog.Load(index)
	if err != nil {
		return err
	}
	ctx := context.Background()
	fp := triplet.New(triplet.DefaultParams())
	spec := func(path string) (fingerprint.Spectrogram, error) {
		pcm, err := audio.Decode(ctx, path, audio.SampleRate)
		if err != nil {
			return fingerprint.Spectrogram{}, err
		}
		return fp.Spectrogram(pcm), nil
	}
	mix, err := spec(audioPath)
	if err != nil {
		return err
	}
	cache := map[int]fingerprint.Spectrogram{}
	ref := func(track int) (fingerprint.Spectrogram, error) {
		if s, ok := cache[track]; ok {
			return s, nil
		}
		path, ok := cat.Audio(track, "")
		if !ok {
			return fingerprint.Spectrogram{}, fmt.Errorf("track %d has no audio", track)
		}
		s, err := spec(path)
		if err != nil {
			return fingerprint.Spectrogram{}, err
		}
		cache[track] = s
		return s, nil
	}
	rng := rand.New(rand.NewPCG(1, 0))
	var pool []fingerprint.Spectrogram
	for _, i := range rng.Perm(len(cat.Tracks)) {
		if len(pool) == poolSize {
			break
		}
		if s, err := ref(i); err == nil {
			pool = append(pool, s)
		}
	}
	fmt.Printf("%-6s %8s %8s %8s %6s %6s %s\n", "start", "py corr", "go corr", "diff", "py z", "go z", "same verdict")
	agree, maxDiff := 0, 0.0
	for k, e := range entries {
		if e.A == nil || e.Corr == nil || e.Z == nil {
			fmt.Printf("%6.0f  (no alignment in the prototype's result)\n", e.Start)
			continue
		}
		r, err := ref(e.Track)
		if err != nil {
			return err
		}
		c := analysis.Verify(mix, r, pool, *e.A, *e.Rate, e.Start, e.End, rand.New(rand.NewPCG(uint64(k), 0)))
		diff := math.Abs(c.Corr - *e.Corr)
		maxDiff = math.Max(maxDiff, diff)
		same := c.Passed() == (*e.Z >= analysis.VerifyMark)
		if same {
			agree++
		}
		fmt.Printf("%6.0f %8.4f %8.4f %8.4f %6.1f %6.1f %v  %s\n", e.Start, *e.Corr, c.Corr, diff, *e.Z, c.Z, same, e.Name)
	}
	fmt.Printf("largest correlation difference %.5f; same verdict on %d of %d\n", maxDiff, agree, len(entries))
	return nil
}
