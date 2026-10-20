// Command tracklist lists the songs in a DJ mix (D31, D32, D36).
//
// It cuts the mix into overlapping windows and searches each one two ways
// (the hybrid matcher, D36): with the tempo-invariant triplet fingerprint
// (dual filterbank, D35) on a triplet index, which fits each match's tempo,
// and with the plain pair fingerprint on a pair index of the same catalog,
// which survives heavy effects better. Each extra factor (-extra, default
// double and half time, beyond the triplet matcher's range) adds a triplet
// search of the window resampled by that factor (turntable) and a pair
// search of its landmarks rescaled by it (keylock). Pair scores are weighted
// by -alpha to put them on the triplet scale, and the decoder turns the
// window answers into entries with start and end times and the tempo change.
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
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/tracklist"
	"github.com/anytroops/landmark/internal/triplet"
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
	if cfg.extra, err = parseFactors(extra); err != nil {
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

// parseFactors reads a comma list of tempo factors, each a ratio of small
// integers (so a turntable undo is an exact resampling).
func parseFactors(s string) ([]float64, error) {
	var out []float64
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		v, err := strconv.ParseFloat(f, 64)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("bad tempo factor %q", f)
		}
		if _, _, ok := ratio(v); !ok {
			return nil, fmt.Errorf("tempo factor %v is not a ratio of integers up to 1000", v)
		}
		out = append(out, v)
	}
	return out, nil
}

// ratio returns p/q = f with q <= 1000.
func ratio(f float64) (p, q int, ok bool) {
	for q = 1; q <= 1000; q++ {
		p = int(math.Round(f * float64(q)))
		if p > 0 && math.Abs(float64(p)/float64(q)-f) < 1e-9 {
			return p, q, true
		}
	}
	return 0, 0, false
}

func dial(addr string) (identifyFunc, func(), error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	client := landmarkv1.NewCoordinatorServiceClient(conn)
	return func(ctx context.Context, req *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
		return client.Identify(ctx, req)
	}, func() { _ = conn.Close() }, nil
}

func run(ctx context.Context, cfg config, file string, w io.Writer) error {
	names, cluster, err := loadCatalog(cfg.index)
	if err != nil {
		return err
	}
	s := searcher{cfg: cfg, cluster: cluster}
	var closeFn func()
	if s.b2, closeFn, err = dial(cfg.coordinator); err != nil {
		return err
	}
	defer closeFn()
	if cfg.pairsCoordinator != "" {
		if err := sameCatalog(cfg.index, cfg.pairsIndex); err != nil {
			return err
		}
		if s.pairs, closeFn, err = dial(cfg.pairsCoordinator); err != nil {
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
		return json.NewEncoder(w).Encode(jsonEntries(entries, names))
	}
	windows := len(windowStarts(len(pcm), int(cfg.window*audio.SampleRate), int(cfg.hop*audio.SampleRate)))
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %.0f s, %d windows, %d searches in %v", filepath.Base(file),
		float64(len(pcm))/audio.SampleRate, windows, len(rows), time.Since(start).Round(time.Millisecond))
	if partial > 0 {
		fmt.Fprintf(&b, " (%d answered by only some partitions)", partial)
	}
	b.WriteString("\n" + formatEntries(entries, names))
	_, err = io.WriteString(w, b.String())
	return err
}

// identifyFunc sends one Identify request.
type identifyFunc func(context.Context, *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error)

// windowStarts returns the start sample of each full window.
func windowStarts(n, window, hop int) []int {
	var out []int
	for a := 0; a+window <= n; a += hop {
		out = append(out, a)
	}
	return out
}

// searcher runs every search of a window.
type searcher struct {
	cfg     config
	b2      identifyFunc
	pairs   identifyFunc // nil: triplet index only
	cluster func(uint32) uint32
}

// fingerprinters holds one goroutine's fingerprinters (not shareable).
type fingerprinters struct {
	trip *triplet.Fingerprinter
	pair *fingerprint.Fingerprinter
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
			fp := fingerprinters{trip: triplet.New(triplet.DefaultParams()),
				pair: fingerprint.New(fingerprint.DefaultParams())}
			for i := range jobs {
				t0 := math.Round(float64(starts[i])/audio.SampleRate*1000) / 1000 // ms, as eval.tracklist
				r, p, err := s.windowRows(ctx, pcm[starts[i]:starts[i]+w], t0, fp)
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

// windowRows runs one window's searches: triplets as is and resampled by
// each extra factor; with a pair index, pairs as is and with landmarks
// rescaled by each extra factor (pair scores weighted by alpha).
func (s searcher) windowRows(ctx context.Context, x []float32, t0 float64, fp fingerprinters) ([]tracklist.Row, int, error) {
	var rows []tracklist.Row
	partial := 0
	ask := func(identify identifyFunc, req *landmarkv1.IdentifyRequest, mode string, factor, weight float64) error {
		if len(req.GetHashes()) == 0 {
			rows = append(rows, tracklist.Row{T0: t0, Mode: mode, Factor: factor, Track: -1})
			return nil
		}
		ctx, cancel := context.WithTimeout(ctx, s.cfg.timeout)
		defer cancel()
		resp, err := identify(ctx, req)
		if err != nil {
			return err
		}
		if resp.GetPartial() {
			partial++
		}
		rows = append(rows, row(t0, mode, factor, weight, resp, s.cluster))
		return nil
	}
	if err := ask(s.b2, request(fp.trip.FingerprintQuery(x, s.cfg.shifts)), "b2", 1, 1); err != nil {
		return nil, 0, err
	}
	for _, f := range s.cfg.extra {
		p, q, _ := ratio(f)
		y := audio.ResamplePoly(x, p, q) // play it 1/f as fast: undoes a turntable change of f
		if err := ask(s.b2, request(fp.trip.FingerprintQuery(y, s.cfg.shifts)), "b2_speed", f, 1); err != nil {
			return nil, 0, err
		}
	}
	if s.pairs == nil {
		return rows, partial, nil
	}
	lms := fp.pair.FingerprintQuery(x, s.cfg.shifts)
	if err := ask(s.pairs, pairRequest(lms), "none", 1, s.cfg.alpha); err != nil {
		return nil, 0, err
	}
	for _, f := range s.cfg.extra {
		if err := ask(s.pairs, pairRequest(fingerprint.RescaleTempo(lms, f)), "keylock", f, s.cfg.alpha); err != nil {
			return nil, 0, err
		}
	}
	return rows, partial, nil
}

// request asks for enough candidates to find a runner-up outside the top
// candidate's duplicate cluster (D14).
func request(lms []triplet.Landmark) *landmarkv1.IdentifyRequest {
	n := len(lms)
	r := &landmarkv1.IdentifyRequest{TopK: 20, Hashes: make([]uint32, n), Times: make([]uint32, n),
		Spans: make([]byte, n), Bands: make([]byte, n)}
	for i, l := range lms {
		r.Hashes[i], r.Times[i], r.Spans[i], r.Bands[i] = l.Hash, l.T, l.Span, l.Band
	}
	return r
}

func pairRequest(lms []fingerprint.Landmark) *landmarkv1.IdentifyRequest {
	n := len(lms)
	r := &landmarkv1.IdentifyRequest{TopK: 20, Hashes: make([]uint32, n), Times: make([]uint32, n)}
	for i, l := range lms {
		r.Hashes[i], r.Times[i] = l.Hash, l.T
	}
	return r
}

// row is one search's answer: the top candidate, the tempo factor (the
// hypothesis's factor times the triplet matcher's fitted scale, when it
// reports one), its offset, and the best score outside its duplicate
// cluster, with scores multiplied by weight.
func row(t0 float64, mode string, factor, weight float64, resp *landmarkv1.IdentifyResponse,
	cluster func(uint32) uint32) tracklist.Row {
	r := tracklist.Row{T0: t0, Mode: mode, Factor: factor, Track: -1}
	cands := resp.GetCandidates()
	if len(cands) == 0 {
		return r
	}
	top := cands[0]
	r.Track, r.Score, r.Offset = int(top.GetTrackId()), weight*float64(top.GetScore()), float64(top.GetOffset())
	if sc := top.GetScale(); sc > 0 {
		r.Factor = factor * float64(sc)
	}
	for _, c := range cands[1:] {
		if cluster(c.GetTrackId()) != cluster(top.GetTrackId()) {
			r.RunnerUp = weight * float64(c.GetScore())
			break
		}
	}
	return r
}

// sameCatalog checks that two indexes number the same tracks the same way,
// so their window answers can be combined.
func sameCatalog(a, b string) error {
	read := func(dir string) ([]catalogTrack, error) {
		raw, err := os.ReadFile(filepath.Join(dir, "catalog.json"))
		if err != nil {
			return nil, fmt.Errorf("read catalog: %w", err)
		}
		var c []catalogTrack
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("parse %s catalog: %w", dir, err)
		}
		return c, nil
	}
	ca, err := read(a)
	if err != nil {
		return err
	}
	cb, err := read(b)
	if err != nil {
		return err
	}
	if len(ca) != len(cb) {
		return fmt.Errorf("catalogs differ: %d tracks in %s, %d in %s", len(ca), a, len(cb), b)
	}
	for i := range ca {
		if ca[i].FMAID != cb[i].FMAID {
			return fmt.Errorf("catalogs differ at track %d: %d in %s, %d in %s", i, ca[i].FMAID, a, cb[i].FMAID, b)
		}
	}
	return nil
}

type catalogTrack struct {
	Dense   uint32 `json:"dense"`
	FMAID   int    `json:"fma_id"`
	Cluster uint32 `json:"cluster"`
}

// loadCatalog reads the index's catalog.json (and extras.json if present)
// and returns a display name and the duplicate cluster for each dense id.
func loadCatalog(dir string) (func(int) string, func(uint32) uint32, error) {
	b, err := os.ReadFile(filepath.Join(dir, "catalog.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("read catalog: %w", err)
	}
	var cat []catalogTrack
	if err := json.Unmarshal(b, &cat); err != nil {
		return nil, nil, fmt.Errorf("parse catalog: %w", err)
	}
	extras := map[int]string{}
	if b, err := os.ReadFile(filepath.Join(dir, "extras.json")); err == nil {
		var ex []struct {
			FMAID int    `json:"fma_id"`
			Name  string `json:"name"`
		}
		if err := json.Unmarshal(b, &ex); err != nil {
			return nil, nil, fmt.Errorf("parse extras: %w", err)
		}
		for _, e := range ex {
			extras[e.FMAID] = e.Name
		}
	}
	names := func(dense int) string {
		if dense < 0 || dense >= len(cat) {
			return fmt.Sprintf("track %d", dense)
		}
		if name, ok := extras[cat[dense].FMAID]; ok {
			return name
		}
		return fmt.Sprintf("FMA %06d", cat[dense].FMAID)
	}
	cluster := func(dense uint32) uint32 {
		if int(dense) < len(cat) {
			return cat[dense].Cluster
		}
		return dense
	}
	return names, cluster, nil
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
