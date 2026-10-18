// Command tracklist lists the songs in a DJ mix (D31 option B2, D32).
//
// It cuts the mix into overlapping windows, fingerprints each with the
// tempo-invariant triplet fingerprint (dual filterbank, D35), asks the
// cluster who each window is (one Identify per window, no speed grid), and
// decodes the window answers into entries with start and end times and the
// estimated tempo change.
//
//	tracklist -coordinator localhost:7500 -index data/segments/medium_unmixdb_b2 mix.mp3
//
// The cluster must serve a triplet index (cmd/ingest -fingerprint
// triplets). -index is that index's directory, for catalog.json (and
// extras.json, the names of non-FMA tracks). Decoder settings default to
// the ones calibrated on UnmixDB (EVAL). Tempo changes beyond the matcher's
// range (about -16% to +19%), such as double-time, are not searched.
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
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/tracklist"
	"github.com/anytroops/landmark/internal/triplet"
)

type config struct {
	coordinator, index string
	window, hop        float64
	shifts, workers    int
	decoder            string
	minEvidence, sw    float64
	tAbs, tRatio       float64
	k                  int
	mergeGap           float64
	timeout            time.Duration
	jsonOut            bool
}

func main() {
	var cfg config
	flag.StringVar(&cfg.coordinator, "coordinator", "", "coordinator gRPC address (required)")
	flag.StringVar(&cfg.index, "index", "", "index directory with catalog.json (required)")
	flag.Float64Var(&cfg.window, "window", 10, "window length, seconds")
	flag.Float64Var(&cfg.hop, "hop", 2.5, "window hop, seconds")
	flag.IntVar(&cfg.shifts, "shifts", 4, "sub-hop query grids (D15)")
	flag.IntVar(&cfg.workers, "workers", 4, "windows fingerprinted and searched at once")
	flag.StringVar(&cfg.decoder, "decoder", "align", "align (line fits + Viterbi) or smooth (majority vote)")
	flag.Float64Var(&cfg.minEvidence, "min-evidence", 50, "align: score a track needs on its line")
	flag.Float64Var(&cfg.sw, "switch", 1, "align: penalty per track change")
	flag.Float64Var(&cfg.tAbs, "t-abs", 10, "smooth: minimum window score")
	flag.Float64Var(&cfg.tRatio, "t-ratio", 3, "smooth: minimum top / runner-up ratio")
	flag.IntVar(&cfg.k, "k", 0, "smooth: neighbors on each side in the majority vote")
	flag.Float64Var(&cfg.mergeGap, "merge-gap", 2.5, "join consecutive entries of one track this close, seconds (-1: off)")
	flag.DurationVar(&cfg.timeout, "timeout", 5*time.Second, "deadline per window")
	flag.BoolVar(&cfg.jsonOut, "json", false, "print entries as JSON")
	flag.Parse()
	if flag.NArg() != 1 || cfg.coordinator == "" || cfg.index == "" ||
		(cfg.decoder != "align" && cfg.decoder != "smooth") {
		fmt.Fprintln(os.Stderr, "usage: tracklist -coordinator addr -index dir [flags] mix-file")
		os.Exit(2)
	}
	if err := run(context.Background(), cfg, flag.Arg(0), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "tracklist:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config, file string, w io.Writer) error {
	names, cluster, err := loadCatalog(cfg.index)
	if err != nil {
		return err
	}
	pcm, err := audio.Decode(ctx, file, audio.SampleRate)
	if err != nil {
		return err
	}
	conn, err := grpc.NewClient(cfg.coordinator, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial %s: %w", cfg.coordinator, err)
	}
	defer func() { _ = conn.Close() }()
	client := landmarkv1.NewCoordinatorServiceClient(conn)

	start := time.Now()
	rows, partial, err := searchWindows(ctx, cfg, pcm, func(ctx context.Context, req *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
		return client.Identify(ctx, req)
	}, cluster)
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
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %.0f s, %d windows searched in %v", filepath.Base(file),
		float64(len(pcm))/audio.SampleRate, len(rows), time.Since(start).Round(time.Millisecond))
	if partial > 0 {
		fmt.Fprintf(&b, " (%d windows answered by only some partitions)", partial)
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

// searchWindows fingerprints and identifies every window on cfg.workers
// goroutines and returns one row per window, plus how many answers were
// partial (a partition down).
func searchWindows(ctx context.Context, cfg config, pcm []float32, identify identifyFunc,
	cluster func(uint32) uint32) ([]tracklist.Row, int, error) {
	w, hop := int(cfg.window*audio.SampleRate), int(cfg.hop*audio.SampleRate)
	starts := windowStarts(len(pcm), w, hop)
	rows := make([]tracklist.Row, len(starts))
	partial := make([]bool, len(starts))
	errs := make([]error, len(starts))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range max(cfg.workers, 1) {
		wg.Go(func() {
			fp := triplet.New(triplet.DefaultParams()) // one per goroutine: not shareable
			for i := range jobs {
				lms := fp.FingerprintQuery(pcm[starts[i]:starts[i]+w], cfg.shifts)
				wctx, cancel := context.WithTimeout(ctx, cfg.timeout)
				resp, err := identify(wctx, request(lms))
				cancel()
				t0 := float64(starts[i]) / audio.SampleRate
				if err != nil {
					errs[i] = fmt.Errorf("window at %.1f s: %w", t0, err)
					continue
				}
				rows[i], partial[i] = row(t0, resp, cluster), resp.GetPartial()
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
		if p {
			n++
		}
	}
	return rows, n, nil
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

// row is one window's answer: the top candidate, its fitted tempo factor
// and offset, and the best score outside its duplicate cluster.
func row(t0 float64, resp *landmarkv1.IdentifyResponse, cluster func(uint32) uint32) tracklist.Row {
	r := tracklist.Row{T0: t0, Mode: "b2", Factor: 1, Track: -1}
	cands := resp.GetCandidates()
	if len(cands) == 0 {
		return r
	}
	top := cands[0]
	r.Track, r.Score = int(top.GetTrackId()), float64(top.GetScore())
	r.Offset, r.Factor = float64(top.GetOffset()), float64(top.GetScale())
	for _, c := range cands[1:] {
		if cluster(c.GetTrackId()) != cluster(top.GetTrackId()) {
			r.RunnerUp = float64(c.GetScore())
			break
		}
	}
	return r
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
