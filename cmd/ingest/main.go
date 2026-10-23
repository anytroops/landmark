// Command ingest fingerprints the indexed tracks of a split manifest and
// writes one segment per partition, plus a catalog of track metadata.
//
// Dense track ids are positions in the manifest's sorted "indexed" list
// (same as the Python prototype). Partition = dense mod P (D2). Decode
// failures are logged, counted, and skipped.
//
// -add list.tsv adds songs to the index in -out while it serves, as the
// next index version (D42): one delta segment per partition that receives
// songs, then the version's catalog and its manifest; then it merges
// segments by the size-tiered policy (D47; -compact does only that). -merge
// folds every partition's deltas into a new base segment as the next
// version.
//
// -extra adds audio files that are not FMA tracks (e.g. a DJ-mix benchmark's
// reference songs), listed one per line as name<TAB>path. They follow the
// manifest's tracks in dense order (or stand alone with -manifest ""), get
// catalog fma_id -1, -2, ... in list order, are their own duplicate clusters,
// and are named in extras.json next to catalog.json.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/runinfo"
	"github.com/anytroops/landmark/internal/segment"
	"github.com/anytroops/landmark/internal/stoplist"
	"github.com/anytroops/landmark/internal/triplet"
)

type manifest struct {
	Meta struct {
		Subset string `json:"subset"`
	} `json:"meta"`
	Indexed  []int            `json:"indexed"`
	Clusters map[string][]int `json:"clusters"`
}

// CatalogTrack is one entry of catalog.json.
type CatalogTrack struct {
	Dense     uint32 `json:"dense"`
	FMAID     int    `json:"fma_id"`
	Partition uint32 `json:"partition"`
	Cluster   uint32 `json:"cluster"` // dense id of the cluster representative (D14)
}

type config struct {
	manifest, data, out string
	extra               string
	partitions          []int
	encoding            segment.Encoding
	triplets            bool // B2 triplet fingerprint into version 2 segments (D33, D34)
	workers             int
}

func main() {
	var cfg config
	var enc, kind string
	flag.StringVar(&cfg.manifest, "manifest", "eval/manifests/splits_small_seed0.json", "split manifest (\"\" for none)")
	flag.StringVar(&cfg.extra, "extra", "", "extra audio files to index, one name<TAB>path per line")
	flag.StringVar(&cfg.data, "data", "data", "dataset root")
	flag.StringVar(&cfg.out, "out", "", "output directory (required); with several -partitions, one subdirectory pN each")
	parts := flag.String("partitions", "1", "partition count P, or a comma list (1,2,4,8) to build several indexes from one fingerprint pass")
	flag.StringVar(&enc, "encoding", "varint", "postings encoding: varint or fixed")
	flag.StringVar(&kind, "fingerprint", "pairs", "landmarks: pairs, or triplets (tempo-invariant, D33)")
	flag.IntVar(&cfg.workers, "workers", runtime.NumCPU(), "decode+fingerprint goroutines")
	add := flag.String("add", "", "add these songs (name<TAB>path lines) to the index in -out as its next version (D42)")
	merge := flag.Bool("merge", false, "merge the index in -out's delta segments into new bases (D42)")
	compactOnly := flag.Bool("compact", false, "merge the index in -out's segments by the size-tiered policy (D47)")
	compactAfterAdd := flag.Bool("compact-after-add", true, "with -add, then merge by the size-tiered policy")
	var pol tierPolicy
	flag.IntVar(&pol.FanIn, "tier-fanin", 4, "size-tiered merging: deltas per tier, and the size ratio between tiers")
	flag.Float64Var(&pol.BaseFraction, "base-fraction", 0.1, "size-tiered merging: fold the deltas into the base once they hold this fraction of its postings")
	flag.IntVar(&pol.MaxDeltas, "max-deltas", 12, "size-tiered merging: most deltas a partition keeps")
	flag.Parse()
	if pol.FanIn < 2 || pol.BaseFraction <= 0 || pol.MaxDeltas < 1 {
		fatal("bad size-tiered policy", "fanin", pol.FanIn, "base_fraction", pol.BaseFraction, "max_deltas", pol.MaxDeltas)
	}
	for _, f := range strings.Split(*parts, ",") {
		p, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || p < 1 {
			fatal("bad -partitions", "value", *parts)
		}
		cfg.partitions = append(cfg.partitions, p)
	}
	switch enc {
	case "varint":
		cfg.encoding = segment.EncodingVarint
	case "fixed":
		cfg.encoding = segment.EncodingFixed
	default:
		fatal("unknown -encoding", "value", enc)
	}
	switch kind {
	case "pairs":
	case "triplets":
		cfg.triplets = true
	default:
		fatal("unknown -fingerprint", "value", kind)
	}
	if cfg.out == "" {
		fatal("need -out")
	}

	// Ctrl-C cancels the context, which kills in-flight ffmpeg processes.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var err error
	switch {
	case *add != "":
		err = addSongs(ctx, cfg, cfg.out, *add)
		if err == nil && *compactAfterAdd {
			err = compact(cfg.out, cfg.encoding, pol.plan)
		}
	case *compactOnly:
		err = compact(cfg.out, cfg.encoding, pol.plan)
	case *merge:
		err = compact(cfg.out, cfg.encoding, mergeAll)
	default:
		err = run(ctx, cfg)
	}
	if err != nil {
		fatal("ingest failed", "err", err)
	}
}

func fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

// fpResult is one track's landmarks as segment entries (Track unset; Span
// and Band zero for pairs).
type fpResult struct {
	dense uint32
	lms   []segment.Entry
	secs  float64
	err   error
}

// Extra is one non-FMA audio file to index (-extra).
type Extra struct {
	Dense uint32 `json:"dense"`
	FMAID int    `json:"fma_id"` // -1, -2, ... in list order
	Name  string `json:"name"`
	Path  string `json:"path"`
}

// parseExtras reads name<TAB>path lines; blank lines and # comments are skipped.
func parseExtras(text string) ([]Extra, error) {
	var out []Extra
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, path, ok := strings.Cut(line, "\t")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(path) == "" {
			return nil, fmt.Errorf("extra list line %d: want name<TAB>path, got %q", i+1, line)
		}
		out = append(out, Extra{FMAID: -(len(out) + 1), Name: strings.TrimSpace(name), Path: strings.TrimSpace(path)})
	}
	return out, nil
}

func run(ctx context.Context, cfg config) error {
	var m manifest
	if cfg.manifest != "" {
		b, err := os.ReadFile(cfg.manifest)
		if err != nil {
			return fmt.Errorf("read manifest: %w", err)
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("parse manifest: %w", err)
		}
	}
	ids := slices.Sorted(slices.Values(m.Indexed))
	var extras []Extra
	if cfg.extra != "" {
		b, err := os.ReadFile(cfg.extra)
		if err != nil {
			return fmt.Errorf("read extra list: %w", err)
		}
		if extras, err = parseExtras(string(b)); err != nil {
			return err
		}
		for k := range extras {
			extras[k].Dense = uint32(len(ids))
			ids = append(ids, extras[k].FMAID)
		}
	}
	if len(ids) == 0 {
		return errors.New("nothing to index: empty manifest and no -extra")
	}
	path := func(id int) string {
		if id < 0 {
			return extras[-id-1].Path
		}
		return trackPath(cfg.data, m.Meta.Subset, id)
	}

	// Fingerprint every track once; each requested P reuses the landmarks.
	start := time.Now()
	lms := make([][]segment.Entry, len(ids))
	var audioSecs float64
	var failures []int
	for r := range fingerprintAll(ctx, cfg, path, ids) {
		if r.err != nil {
			if errors.Is(r.err, audio.ErrDecode) {
				slog.Warn("decode failed, skipping", "fma_id", ids[r.dense], "err", r.err)
				failures = append(failures, ids[r.dense])
				continue
			}
			return r.err // context cancelled or ffmpeg missing: stop
		}
		audioSecs += r.secs
		lms[r.dense] = r.lms
	}
	fpSecs := time.Since(start).Seconds()
	slog.Info("fingerprinted", "tracks", len(ids), "failures", len(failures), "audio_s", int(audioSecs), "wall_s", int(fpSecs))

	for _, nParts := range cfg.partitions {
		dir := cfg.out
		if len(cfg.partitions) > 1 {
			dir = filepath.Join(cfg.out, fmt.Sprintf("p%d", nParts))
		}
		stats := ingestStats{
			Manifest: cfg.manifest, Partitions: nParts, Encoding: encName(cfg.encoding), Fingerprint: kindName(cfg.triplets),
			Workers: cfg.workers, Tracks: len(ids), DecodeFailures: failures, AudioSeconds: audioSecs,
			FingerprintWallSeconds: fpSecs, Machine: runinfo.Machine("."),
		}
		if err := writeIndex(dir, uint32(nParts), cfg, ids, m.Clusters, lms, &stats); err != nil {
			return err
		}
		if len(extras) > 0 {
			if err := writeJSON(filepath.Join(dir, "extras.json"), extras); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeIndex writes one segment per partition plus catalog.json, index.json,
// and ingest_stats.json into dir.
func writeIndex(dir string, nParts uint32, cfg config, ids []int, clusters map[string][]int,
	lms [][]segment.Entry, stats *ingestStats) error {
	start := time.Now()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	parts := make([][]segment.Entry, nParts)
	for d, ls := range lms {
		p, track := uint32(d)%nParts, uint32(d)/nParts
		for _, e := range ls {
			e.Track = track
			parts[p] = append(parts[p], e)
		}
	}
	manifest := IndexManifest{NPartitions: nParts, NTracks: len(ids), Fingerprint: kindName(cfg.triplets)}
	for p := range nParts {
		nTracks := (uint32(len(ids)) + nParts - 1 - p) / nParts // dense ids d < N with d mod P == p
		meta := segment.Meta{Partition: p, NPartitions: nParts, NTracks: nTracks, Encoding: cfg.encoding, Aux: cfg.triplets}
		path := filepath.Join(dir, fmt.Sprintf("part-%03d.lmrk", p))
		n := len(parts[p])
		if err := segment.Write(path, meta, parts[p]); err != nil {
			return fmt.Errorf("index with %d partitions, partition %d: %w", nParts, p, err)
		}
		parts[p] = nil // let the GC reclaim it before the next partition
		seg, err := segment.Open(path)
		if err != nil {
			return fmt.Errorf("reopen index with %d partitions, partition %d: %w", nParts, p, err)
		}
		crc := fmt.Sprintf("%08x", seg.Checksum())
		stats.Segments = append(stats.Segments, segStats{
			Partition: p, Path: path, Keys: seg.NKeys(), Postings: n, Bytes: seg.SizeBytes(), CRC32C: crc,
		})
		manifest.Segments = append(manifest.Segments, ManifestEntry{Partition: p, File: filepath.Base(path), CRC32C: crc})
		stats.TotalBytes += seg.SizeBytes()
		stats.TotalPostings += n
		_ = seg.Close()
	}
	stats.BytesPerPosting = float64(stats.TotalBytes) / float64(max(stats.TotalPostings, 1))
	stats.WriteWallSeconds = time.Since(start).Seconds()

	stop, err := writeStoplist(dir, manifest.Segments, uint32(len(ids)))
	if err != nil {
		return fmt.Errorf("index with %d partitions: %w", nParts, err)
	}
	manifest.Stoplist = stop

	if err := writeJSON(filepath.Join(dir, "catalog.json"), buildCatalog(ids, clusters, nParts)); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "index.json"), manifest); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "ingest_stats.json"), stats); err != nil {
		return err
	}
	slog.Info("wrote index", "dir", dir, "partitions", nParts, "postings", stats.TotalPostings,
		"bytes", stats.TotalBytes, "bytes_per_posting", fmt.Sprintf("%.3f", stats.BytesPerPosting),
		"write_s", fmt.Sprintf("%.1f", stats.WriteWallSeconds))
	return nil
}

// writeStoplist counts every hash's postings across the segments just
// written and saves the stop list (D30) next to them.
func writeStoplist(dir string, entries []ManifestEntry, nTracks uint32) (*stoplist.ManifestEntry, error) {
	segs := make([]*segment.Segment, 0, len(entries))
	defer func() {
		for _, s := range segs {
			_ = s.Close()
		}
	}()
	for _, e := range entries {
		s, err := segment.Open(filepath.Join(dir, e.File))
		if err != nil {
			return nil, err
		}
		segs = append(segs, s)
	}
	l, err := stoplist.FromSegments(segs, stoplist.MinCountFor(nTracks, stoplist.DefaultMinFraction))
	if err != nil {
		return nil, err
	}
	if err := l.Write(filepath.Join(dir, stoplist.FileName)); err != nil {
		return nil, err
	}
	return &stoplist.ManifestEntry{File: stoplist.FileName, CRC32C: fmt.Sprintf("%08x", l.Checksum())}, nil
}

// fingerprintAll decodes and fingerprints tracks on cfg.workers goroutines.
// The returned channel closes once every track has a result.
func fingerprintAll(ctx context.Context, cfg config, path func(int) string, ids []int) <-chan fpResult {
	jobs := make(chan uint32)
	results := make(chan fpResult, cfg.workers)
	var wg sync.WaitGroup
	for range cfg.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// One fingerprinter per goroutine: they are not shareable.
			var pairs *fingerprint.Fingerprinter
			var triplets *triplet.Fingerprinter
			if cfg.triplets {
				triplets = triplet.New(triplet.DefaultParams())
			} else {
				pairs = fingerprint.New(fingerprint.DefaultParams())
			}
			for dense := range jobs {
				pcm, err := audio.Decode(ctx, path(ids[dense]), audio.SampleRate)
				r := fpResult{dense: dense, err: err}
				if err == nil {
					r.lms = entries(pairs, triplets, pcm)
					r.secs = float64(len(pcm)) / audio.SampleRate
				}
				results <- r
			}
		}()
	}
	go func() {
		defer close(jobs)
		for dense := range ids {
			select {
			case jobs <- uint32(dense):
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	return results
}

// entries fingerprints pcm with whichever fingerprinter is set.
func entries(pairs *fingerprint.Fingerprinter, triplets *triplet.Fingerprinter, pcm []float32) []segment.Entry {
	if triplets != nil {
		lms := triplets.Fingerprint(pcm)
		out := make([]segment.Entry, len(lms))
		for i, l := range lms {
			out[i] = segment.Entry{Hash: l.Hash, T: l.T, Span: l.Span, Band: l.Band}
		}
		return out
	}
	lms := pairs.Fingerprint(pcm)
	out := make([]segment.Entry, len(lms))
	for i, l := range lms {
		out[i] = segment.Entry{Hash: l.Hash, T: l.T}
	}
	return out
}

func kindName(triplets bool) string {
	if triplets {
		return "triplets"
	}
	return "pairs"
}

func buildCatalog(ids []int, clusters map[string][]int, nParts uint32) []CatalogTrack {
	dense := make(map[int]uint32, len(ids))
	for i, id := range ids {
		dense[id] = uint32(i)
	}
	rep := make(map[uint32]uint32)
	for _, members := range clusters {
		var ds []uint32
		for _, f := range members {
			if d, ok := dense[f]; ok {
				ds = append(ds, d)
			}
		}
		if len(ds) == 0 {
			continue
		}
		lo := slices.Min(ds)
		for _, d := range ds {
			rep[d] = lo
		}
	}
	out := make([]CatalogTrack, len(ids))
	for i, id := range ids {
		d := uint32(i)
		c, ok := rep[d]
		if !ok {
			c = d
		}
		out[i] = CatalogTrack{Dense: d, FMAID: id, Partition: d % nParts, Cluster: c}
	}
	return out
}

func trackPath(data, subset string, id int) string {
	s := fmt.Sprintf("%06d", id)
	return filepath.Join(data, "fma_"+subset, s[:3], s+".mp3")
}

type segStats struct {
	Partition uint32 `json:"partition"`
	Path      string `json:"path"`
	Keys      int    `json:"keys"`
	Postings  int    `json:"postings"`
	Bytes     int    `json:"bytes"`
	CRC32C    string `json:"crc32c"`
}

// IndexManifest (index.json) describes a built index: one segment per
// partition and its checksum. The coordinator checks replicas against it (D26).
type IndexManifest struct {
	NPartitions uint32                  `json:"n_partitions"`
	NTracks     int                     `json:"n_tracks"`
	Fingerprint string                  `json:"fingerprint"` // pairs or triplets: what queries must send
	Segments    []ManifestEntry         `json:"segments"`
	Stoplist    *stoplist.ManifestEntry `json:"stoplist,omitempty"`
}

// ManifestEntry is one partition's segment.
type ManifestEntry struct {
	Partition uint32 `json:"partition"`
	File      string `json:"file"`
	CRC32C    string `json:"crc32c"` // 8 hex digits
}

type ingestStats struct {
	Manifest               string            `json:"manifest"`
	Partitions             int               `json:"partitions"`
	Encoding               string            `json:"encoding"`
	Fingerprint            string            `json:"fingerprint"`
	Workers                int               `json:"workers"`
	Tracks                 int               `json:"tracks"`
	DecodeFailures         []int             `json:"decode_failures"`
	AudioSeconds           float64           `json:"audio_seconds"`
	Segments               []segStats        `json:"segments"`
	TotalPostings          int               `json:"total_postings"`
	TotalBytes             int               `json:"total_bytes"`
	BytesPerPosting        float64           `json:"bytes_per_posting"`
	FingerprintWallSeconds float64           `json:"fingerprint_wall_seconds"`
	WriteWallSeconds       float64           `json:"write_wall_seconds"`
	Machine                map[string]string `json:"machine"`
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func encName(e segment.Encoding) string {
	if e == segment.EncodingFixed {
		return "fixed"
	}
	return "varint"
}
