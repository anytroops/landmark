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
// EVAL, hybrid section), which were tuned with the pair index searched and
// both clusters' stop lists at 4% of tracks (stop_fraction 0.04, D30): the
// scores, and so the thresholds, depend on it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	analyze                      bool   // D43: fades, turntable or keylock, BPM
	fmaDir                       string // FMA references' audio, for -analyze
	rowsPath                     string // also write every search's row here (CSV)
	snap                         int    // start each window's audio on a multiple of this many samples (0: off)
	offset                       int    // start each window this many samples late (an experiment)
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
	flag.BoolVar(&cfg.analyze, "analyze", false, "also estimate each song's fades, turntable or keylock, and BPM in the set (needs the references' audio)")
	flag.IntVar(&cfg.snap, "snap", 0, "experiment (docs/design/throughput.md T1): start each window's audio at the next multiple of this many samples, keeping the hop (128 puts every speed's frames on one shared grid)")
	flag.IntVar(&cfg.offset, "offset", 0, "experiment: start each window this many samples late (before -snap), to see how accuracy varies with window placement")
	flag.StringVar(&cfg.rowsPath, "rows", "", "also write every window search's answer here, as CSV (eval.tracklist's columns), e.g. to calibrate the decoder")
	flag.StringVar(&cfg.fmaDir, "fma-dir", "data/fma_medium", "with -analyze: FMA tracks' audio (non-FMA references use the paths in extras.json)")
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
	if cfg.rowsPath != "" {
		if err := writeRows(cfg.rowsPath, rows); err != nil {
			return err
		}
	}
	var entries []tracklist.Entry
	if cfg.decoder == "align" {
		entries = tracklist.Align(rows, cfg.window, cfg.minEvidence, cfg.sw)
	} else {
		entries = tracklist.Smooth(rows, cfg.window, cfg.tAbs, cfg.tRatio, cfg.k)
	}
	if cfg.mergeGap >= 0 {
		entries = tracklist.MergeRepeats(entries, cfg.mergeGap)
	}
	var an map[int]songAnalysis
	if cfg.analyze {
		bpm, err := readBPM(cfg.index)
		if err != nil {
			return err
		}
		if an, err = analyzeMix(ctx, pcm, rows, entries, cat, cfg.fmaDir, bpm); err != nil {
			return err
		}
	}
	if cfg.jsonOut {
		return json.NewEncoder(w).Encode(jsonEntries(entries, cat.Name, an))
	}
	windows := len(windowStarts(len(pcm), int(cfg.window*audio.SampleRate), int(cfg.hop*audio.SampleRate)))
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %.0f s, %d windows, %d searches in %v", filepath.Base(file),
		float64(len(pcm))/audio.SampleRate, windows, len(rows), time.Since(start).Round(time.Millisecond))
	if partial > 0 {
		fmt.Fprintf(&b, " (%d answered by only some partitions)", partial)
	}
	b.WriteString("\n" + formatEntries(entries, cat.Name, an))
	_, err = io.WriteString(w, b.String())
	return err
}

// writeRows writes rows as CSV with eval.tracklist's columns (less the mix
// index and landmark count): scores as the decoder saw them, so run with
// -alpha 1 to get the pair searches' own scores.
func writeRows(path string, rows []tracklist.Row) error {
	var b strings.Builder
	b.WriteString("t0,mode,factor,top1_dense,score,runner_up,offset\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s,%s,%s,%d,%s,%s,%s\n", fmtFloat(r.T0), r.Mode, fmtFloat(r.Factor), r.Track,
			fmtFloat(r.Score), fmtFloat(r.RunnerUp), fmtFloat(r.Offset))
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func fmtFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// placeStarts moves each window start offset samples later, then up to the
// next multiple of grid (0: none), dropping any that would then start
// after last.
func placeStarts(starts []int, offset, grid, last int) []int {
	var out []int
	for _, a := range starts {
		a += offset
		if grid > 0 {
			a = (a + grid - 1) / grid * grid
		}
		if a <= last {
			out = append(out, a)
		}
	}
	return out
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
	if s.cfg.snap > 0 || s.cfg.offset > 0 {
		starts = placeStarts(starts, s.cfg.offset, s.cfg.snap, len(pcm)-w)
	}
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
	// With -analyze (D43): fades as [start_s, end_s], whether the tempo
	// change was a turntable's (pitch moved) or keylock, and the BPM in
	// the set.
	FadeIn    []float64 `json:"fade_in,omitempty"`
	FadeOut   []float64 `json:"fade_out,omitempty"`
	Turntable *bool     `json:"turntable,omitempty"`
	BPM       float64   `json:"bpm,omitempty"`
}

func jsonEntries(entries []tracklist.Entry, names func(int) string, an map[int]songAnalysis) []jsonEntry {
	out := make([]jsonEntry, len(entries))
	for i, e := range entries {
		out[i] = jsonEntry{Track: e.Track, Name: names(e.Track), StartS: e.Start, EndS: e.End,
			TempoPct: 100 * (e.Factor - 1), Evidence: e.Evidence, StartClock: clock(e.Start)}
		if a, ok := an[i]; ok {
			out[i].FadeIn, out[i].FadeOut, out[i].BPM = a.FadeIn, a.FadeOut, a.BPM
			out[i].Turntable = &a.Turntable
		}
	}
	return out
}

func formatEntries(entries []tracklist.Entry, names func(int) string, an map[int]songAnalysis) string {
	if len(entries) == 0 {
		return "no tracks found\n"
	}
	var b strings.Builder
	for i, e := range entries {
		fmt.Fprintf(&b, "%2d. %s-%s  %-52s %+5.1f%%  (evidence %.0f)",
			i+1, clock(e.Start), clock(e.End), names(e.Track), 100*(e.Factor-1), e.Evidence)
		if a, ok := an[i]; ok {
			b.WriteString(describe(a))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// describe summarizes one song's analysis for the text output.
func describe(a songAnalysis) string {
	var b strings.Builder
	if a.FadeIn != nil {
		fmt.Fprintf(&b, "  in %s-%s", clock(a.FadeIn[0]), clock(a.FadeIn[1]))
	}
	if a.FadeOut != nil {
		fmt.Fprintf(&b, "  out %s-%s", clock(a.FadeOut[0]), clock(a.FadeOut[1]))
	}
	switch { // only a real tempo change has a kind
	case math.Abs(a.Rate-1) <= 0.005:
	case a.Turntable:
		b.WriteString("  turntable")
	default:
		b.WriteString("  keylock")
	}
	if a.BPM > 0 {
		fmt.Fprintf(&b, "  %.1f BPM", a.BPM)
	}
	return b.String()
}

// clock formats seconds as m:ss.
func clock(s float64) string {
	t := int(s + 0.5)
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}
