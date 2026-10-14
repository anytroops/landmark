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
	"sync"
	"time"

	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/runinfo"
	"github.com/anytroops/landmark/internal/segment"
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
	partitions          int
	encoding            segment.Encoding
	globalIDs           bool
	workers             int
}

func main() {
	var cfg config
	var enc string
	flag.StringVar(&cfg.manifest, "manifest", "eval/manifests/splits_small_seed0.json", "split manifest")
	flag.StringVar(&cfg.data, "data", "data", "dataset root")
	flag.StringVar(&cfg.out, "out", "", "output directory (required)")
	flag.IntVar(&cfg.partitions, "partitions", 1, "number of partitions P")
	flag.StringVar(&enc, "encoding", "varint", "postings encoding: varint or fixed")
	flag.BoolVar(&cfg.globalIDs, "global-ids", false, "store global track ids (B4 comparison only; shards expect local)")
	flag.IntVar(&cfg.workers, "workers", runtime.NumCPU(), "decode+fingerprint goroutines")
	flag.Parse()
	switch enc {
	case "varint":
		cfg.encoding = segment.EncodingVarint
	case "fixed":
		cfg.encoding = segment.EncodingFixed
	default:
		fatal("unknown -encoding", "value", enc)
	}
	if cfg.out == "" || cfg.partitions < 1 {
		fatal("need -out and -partitions >= 1")
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
	catalog := buildCatalog(ids, m.Clusters, uint32(cfg.partitions))

	start := time.Now()
	results := fingerprintAll(ctx, cfg, m.Meta.Subset, ids)

	// One entry slice per partition. Only this goroutine appends, so no locks.
	nParts := uint32(cfg.partitions)
	parts := make([][]segment.Entry, nParts)
	var audioSecs float64
	var failures []int
	for r := range results {
		if r.err != nil {
			if errors.Is(r.err, audio.ErrDecode) {
				slog.Warn("decode failed, skipping", "fma_id", ids[r.dense], "err", r.err)
				failures = append(failures, ids[r.dense])
				continue
			}
			return r.err // context cancelled or ffmpeg missing: stop
		}
		audioSecs += r.secs
		p := r.dense % nParts
		track := r.dense / nParts
		if cfg.globalIDs {
			track = r.dense
		}
		for _, lm := range r.lms {
			parts[p] = append(parts[p], segment.Entry{Hash: lm.Hash, Track: track, T: lm.T})
		}
	}
	fpSecs := time.Since(start).Seconds()

	if err := os.MkdirAll(cfg.out, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	stats := ingestStats{
		Manifest: cfg.manifest, Partitions: cfg.partitions, Encoding: encName(cfg.encoding),
		GlobalIDs: cfg.globalIDs, Workers: cfg.workers, Tracks: len(ids),
		DecodeFailures: failures, AudioSeconds: audioSecs, FingerprintWallSeconds: fpSecs,
		Machine: runinfo.Machine("."),
	}
	manifest := IndexManifest{NPartitions: nParts, NTracks: len(ids)}
	for p := range nParts {
		nTracks := (uint32(len(ids)) + nParts - 1 - p) / nParts // dense ids d < N with d mod P == p
		meta := segment.Meta{Partition: p, NPartitions: nParts, NTracks: nTracks, Encoding: cfg.encoding}
		if cfg.globalIDs {
			meta = segment.Meta{Partition: 0, NPartitions: 1, NTracks: uint32(len(ids)), Encoding: cfg.encoding}
		}
		path := filepath.Join(cfg.out, fmt.Sprintf("part-%03d.lmrk", p))
		n := len(parts[p])
		if err := segment.Write(path, meta, parts[p]); err != nil {
			return fmt.Errorf("partition %d: %w", p, err)
		}
		parts[p] = nil // let the GC reclaim it before the next partition
		seg, err := segment.Open(path)
		if err != nil {
			return fmt.Errorf("reopen partition %d: %w", p, err)
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
	stats.TotalWallSeconds = time.Since(start).Seconds()

	if err := writeJSON(filepath.Join(cfg.out, "catalog.json"), catalog); err != nil {
		return err
	}
	if !cfg.globalIDs { // global-id builds are for size measurement only, never served
		if err := writeJSON(filepath.Join(cfg.out, "index.json"), manifest); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(cfg.out, "ingest_stats.json"), stats); err != nil {
		return err
	}
	slog.Info("done", "tracks", len(ids), "failures", len(failures), "postings", stats.TotalPostings,
		"bytes", stats.TotalBytes, "bytes_per_posting", fmt.Sprintf("%.3f", stats.BytesPerPosting),
		"wall_s", fmt.Sprintf("%.1f", stats.TotalWallSeconds))
	return nil
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
	NPartitions uint32          `json:"n_partitions"`
	NTracks     int             `json:"n_tracks"`
	Segments    []ManifestEntry `json:"segments"`
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
	GlobalIDs              bool              `json:"global_ids"`
	Workers                int               `json:"workers"`
	Tracks                 int               `json:"tracks"`
	DecodeFailures         []int             `json:"decode_failures"`
	AudioSeconds           float64           `json:"audio_seconds"`
	Segments               []segStats        `json:"segments"`
	TotalPostings          int               `json:"total_postings"`
	TotalBytes             int               `json:"total_bytes"`
	BytesPerPosting        float64           `json:"bytes_per_posting"`
	FingerprintWallSeconds float64           `json:"fingerprint_wall_seconds"`
	TotalWallSeconds       float64           `json:"total_wall_seconds"`
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
