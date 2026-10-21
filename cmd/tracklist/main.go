// Command tracklist lists the songs in a DJ mix (D31, D32, D36).
//
// It cuts the mix into overlapping windows, searches each with the hybrid
// matcher (internal/hybrid: triplet and pair indexes of one catalog, plus
// double and half time), and the decoder turns the window answers into
// entries with start and end times and the tempo change.
//
//	tracklist -coordinator localhost:7500 -index data/segments/medium_b2 \
//	    -pairs-coordinator localhost:7600 -pairs-index data/segments/medium mix.mp3
//
// Without -pairs-coordinator it searches the triplet index only. Each -index
// is the index's directory, for catalog.json (and extras.json, the names of
// non-FMA tracks); both must number tracks the same way. Decoder settings
// and -alpha default to the hybrid's calibration on UnmixDB set222 and
// set275 (eval/results/tracklist/calibration_unmixdb_s8_b2dhp_hybrid_*;
// EVAL, hybrid section), which were tuned with the pair index searched.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/catalog"
	"github.com/anytroops/landmark/internal/hybrid"
	"github.com/anytroops/landmark/internal/tracklist"
)

type config struct {
	coordinator, index           string
	pairsCoordinator, pairsIndex string
	window, hop                  float64
	shifts, workers              int
	extra                        []float64
	alpha                        float64
	decoder                      string
	minEvidence, sw              float64
	tAbs, tRatio                 float64
	k                            int
	mergeGap                     float64
	timeout                      time.Duration
	jsonOut                      bool
}

func main() {
	var cfg config
	var extra string
	flag.StringVar(&cfg.coordinator, "coordinator", "", "coordinator of the triplet index (required)")
	flag.StringVar(&cfg.index, "index", "", "triplet index directory, for catalog.json (required)")
	flag.StringVar(&cfg.pairsCoordinator, "pairs-coordinator", "", "coordinator of the pair index (hybrid, D36)")
	flag.StringVar(&cfg.pairsIndex, "pairs-index", "", "pair index directory (with -pairs-coordinator)")
	flag.Float64Var(&cfg.window, "window", 10, "window length, seconds")
	flag.Float64Var(&cfg.hop, "hop", 2.5, "window hop, seconds")
	flag.IntVar(&cfg.shifts, "shifts", 4, "sub-hop query grids (D15)")
	flag.IntVar(&cfg.workers, "workers", 4, "windows fingerprinted and searched at once")
	flag.StringVar(&extra, "extra", "2,0.5", "extra tempo factors searched per window (empty: none)")
	flag.Float64Var(&cfg.alpha, "alpha", 3, "weight of pair scores against triplet scores")
	flag.StringVar(&cfg.decoder, "decoder", "smooth", "smooth (majority vote) or align (line fits + Viterbi)")
	flag.Float64Var(&cfg.minEvidence, "min-evidence", 100, "align: score a track needs on its line")
	flag.Float64Var(&cfg.sw, "switch", 1, "align: penalty per track change")
	flag.Float64Var(&cfg.tAbs, "t-abs", 10, "smooth: minimum window score")
	flag.Float64Var(&cfg.tRatio, "t-ratio", 2, "smooth: minimum top / runner-up ratio")
	flag.IntVar(&cfg.k, "k", 1, "smooth: neighbors on each side in the majority vote")
	flag.Float64Var(&cfg.mergeGap, "merge-gap", 2.5, "join consecutive entries of one track this close, seconds (-1: off)")
	flag.DurationVar(&cfg.timeout, "timeout", 5*time.Second, "deadline per window")
	flag.BoolVar(&cfg.jsonOut, "json", false, "print entries as JSON")
	flag.Parse()
	var err error
	if cfg.extra, err = hybrid.ParseFactors(extra); err != nil {
		fmt.Fprintln(os.Stderr, "tracklist:", err)
		os.Exit(2)
	}
	if flag.NArg() != 1 || cfg.coordinator == "" || cfg.index == "" ||
		(cfg.pairsCoordinator == "") != (cfg.pairsIndex == "") ||
		(cfg.decoder != "align" && cfg.decoder != "smooth") {
		fmt.Fprintln(os.Stderr, "usage: tracklist -coordinator addr -index dir [-pairs-coordinator addr -pairs-index dir] [flags] mix-file")
		os.Exit(2)
	}
	if err := run(context.Background(), cfg, flag.Arg(0), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "tracklist:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config, file string, w io.Writer) error {
	cat, err := catalog.Load(cfg.index)
	if err != nil {
		return err
	}
	s := searcher{cfg: cfg, hybrid: &hybrid.Searcher{Cluster: cat.Cluster, Shifts: cfg.shifts,
		Extra: cfg.extra, Alpha: cfg.alpha, Timeout: cfg.timeout}}
	var closeFn func()
	if s.hybrid.B2, closeFn, err = hybrid.Dial(cfg.coordinator); err != nil {
		return err
	}
	defer closeFn()
	if cfg.pairsCoordinator != "" {
		if err := catalog.Same(cfg.index, cfg.pairsIndex); err != nil {
			return err
		}
		if s.hybrid.Pairs, closeFn, err = hybrid.Dial(cfg.pairsCoordinator); err != nil {
			return err
		}
		defer closeFn()
	}
	pcm, err := audio.Decode(ctx, file, audio.SampleRate)
	if err != nil {
		return err
	}

	start := time.Now()
	rows, partial, err := s.searchWindows(ctx, pcm)
	if err != nil {
		return err
	}
	tracklist.SortRows(rows)
	var entries []tracklist.Entry
	if cfg.decoder == "align" {
		entries = tracklist.Align(rows, cfg.window, cfg.minEvidence, cfg.sw)
	} else {
		entries = tracklist.Smooth(rows, cfg.window, cfg.tAbs, cfg.tRatio, cfg.k)
	}
	if cfg.mergeGap >= 0 {
		entries = tracklist.MergeRepeats(entries, cfg.mergeGap)
	}
	if cfg.jsonOut {
		return json.NewEncoder(w).Encode(jsonEntries(entries, cat.Name))
	}
	windows := len(windowStarts(len(pcm), int(cfg.window*audio.SampleRate), int(cfg.hop*audio.SampleRate)))
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %.0f s, %d windows, %d searches in %v", filepath.Base(file),
		float64(len(pcm))/audio.SampleRate, windows, len(rows), time.Since(start).Round(time.Millisecond))
	if partial > 0 {
		fmt.Fprintf(&b, " (%d answered by only some partitions)", partial)
	}
	b.WriteString("\n" + formatEntries(entries, cat.Name))
	_, err = io.WriteString(w, b.String())
	return err
}

// windowStarts returns the start sample of each full window.
func windowStarts(n, window, hop int) []int {
	var out []int
	for a := 0; a+window <= n; a += hop {
		out = append(out, a)
	}
	return out
}

// searcher searches every window of a mix.
type searcher struct {
	cfg    config
	hybrid *hybrid.Searcher
}

// searchWindows searches every window on cfg.workers goroutines and returns
// all rows, plus how many answers were partial (a partition down).
func (s searcher) searchWindows(ctx context.Context, pcm []float32) ([]tracklist.Row, int, error) {
	w, hop := int(s.cfg.window*audio.SampleRate), int(s.cfg.hop*audio.SampleRate)
	starts := windowStarts(len(pcm), w, hop)
	rows := make([][]tracklist.Row, len(starts))
	partial := make([]int, len(starts))
	errs := make([]error, len(starts))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range max(s.cfg.workers, 1) {
		wg.Go(func() {
			fp := hybrid.NewFingerprinters()
			for i := range jobs {
				t0 := hybrid.WindowT0(starts[i])
				r, p, err := s.hybrid.Window(ctx, pcm[starts[i]:starts[i]+w], t0, fp)
				if err != nil {
					errs[i] = fmt.Errorf("window at %.1f s: %w", t0, err)
					continue
				}
				rows[i], partial[i] = r, p
			}
		})
	}
	for i := range starts {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, 0, err
	}
	n := 0
	for _, p := range partial {
		n += p
	}
	return slices.Concat(rows...), n, nil
}

type jsonEntry struct {
	Track      int     `json:"track"`
	Name       string  `json:"name"`
	StartS     float64 `json:"start_s"`
	EndS       float64 `json:"end_s"`
	TempoPct   float64 `json:"tempo_pct"`
	Evidence   float64 `json:"evidence"`
	StartClock string  `json:"start"`
}

func jsonEntries(entries []tracklist.Entry, names func(int) string) []jsonEntry {
	out := make([]jsonEntry, len(entries))
	for i, e := range entries {
		out[i] = jsonEntry{Track: e.Track, Name: names(e.Track), StartS: e.Start, EndS: e.End,
			TempoPct: 100 * (e.Factor - 1), Evidence: e.Evidence, StartClock: clock(e.Start)}
	}
	return out
}

func formatEntries(entries []tracklist.Entry, names func(int) string) string {
	if len(entries) == 0 {
		return "no tracks found\n"
	}
	var b strings.Builder
	for i, e := range entries {
		fmt.Fprintf(&b, "%2d. %s-%s  %-52s %+5.1f%%  (evidence %.0f)\n",
			i+1, clock(e.Start), clock(e.End), names(e.Track), 100*(e.Factor-1), e.Evidence)
	}
	return b.String()
}

// clock formats seconds as m:ss.
func clock(s float64) string {
	t := int(s + 0.5)
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}
