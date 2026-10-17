// Command ingest fingerprints the indexed tracks of a split manifest and
// writes one segment per partition, plus a catalog of track metadata.
//
// Dense track ids are positions in the manifest's sorted "indexed" list
// (same as the Python prototype). Partition = dense mod P (D2). Decode
// failures are logged, counted, and skipped.
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
	partitions          []int
	encoding            segment.Encoding
	workers             int
}

func main() {
	var cfg config
	var enc string
	flag.StringVar(&cfg.manifest, "manifest", "eval/manifests/splits_small_seed0.json", "split manifest")
	flag.StringVar(&cfg.data, "data", "data", "dataset root")
	flag.StringVar(&cfg.out, "out", "", "output directory (required); with several -partitions, one subdirectory pN each")
	parts := flag.String("partitions", "1", "partition count P, or a comma list (1,2,4,8) to build several indexes from one fingerprint pass")
	flag.StringVar(&enc, "encoding", "varint", "postings encoding: varint or fixed")
	flag.IntVar(&cfg.workers, "workers", runtime.NumCPU(), "decode+fingerprint goroutines")
	flag.Parse()
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
	if cfg.out == "" {
		fatal("need -out")
	}

	// Ctrl-C cancels the context, which kills in-flight ffmpeg processes.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		fatal("ingest failed", "err", err)
	}
}

func fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

type fpResult struct {
	dense uint32
	lms   []fingerprint.Landmark
	secs  float64
	err   error
}

func run(ctx context.Context, cfg config) error {
	b, err := os.ReadFile(cfg.manifest)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	ids := slices.Sorted(slices.Values(m.Indexed))

	// Fingerprint every track once; each requested P reuses the landmarks.
	start := time.Now()
	lms := make([][]fingerprint.Landmark, len(ids))
	var audioSecs float64
	var failures []int
	for r := range fingerprintAll(ctx, cfg, m.Meta.Subset, ids) {
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
			Manifest: cfg.manifest, Partitions: nParts, Encoding: encName(cfg.encoding),
			Workers: cfg.workers, Tracks: len(ids), DecodeFailures: failures, AudioSeconds: audioSecs,
			FingerprintWallSeconds: fpSecs, Machine: runinfo.Machine("."),
		}
		if err := writeIndex(dir, uint32(nParts), cfg.encoding, ids, m.Clusters, lms, &stats); err != nil {
			return err
		}
	}
	return nil
}

// writeIndex writes one segment per partition plus catalog.json, index.json,
// and ingest_stats.json into dir.
func writeIndex(dir string, nParts uint32, enc segment.Encoding, ids []int, clusters map[string][]int,
	lms [][]fingerprint.Landmark, stats *ingestStats) error {
	start := time.Now()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	parts := make([][]segment.Entry, nParts)
	for d, ls := range lms {
		p, track := uint32(d)%nParts, uint32(d)/nParts
		for _, lm := range ls {
			parts[p] = append(parts[p], segment.Entry{Hash: lm.Hash, Track: track, T: lm.T})
		}
	}
	manifest := IndexManifest{NPartitions: nParts, NTracks: len(ids)}
	for p := range nParts {
		nTracks := (uint32(len(ids)) + nParts - 1 - p) / nParts // dense ids d < N with d mod P == p
		meta := segment.Meta{Partition: p, NPartitions: nParts, NTracks: nTracks, Encoding: enc}
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
func fingerprintAll(ctx context.Context, cfg config, subset string, ids []int) <-chan fpResult {
	jobs := make(chan uint32)
	results := make(chan fpResult, cfg.workers)
	var wg sync.WaitGroup
	for range cfg.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fp := fingerprint.New(fingerprint.DefaultParams()) // one per goroutine: not shareable
			for dense := range jobs {
				pcm, err := audio.Decode(ctx, trackPath(cfg.data, subset, ids[dense]), audio.SampleRate)
				r := fpResult{dense: dense, err: err}
				if err == nil {
					r.lms = fp.Fingerprint(pcm)
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
