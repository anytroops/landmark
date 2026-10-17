// Package cluster loads the static cluster description the coordinator
// serves from: which replicas hold which partition, what each partition's
// segment must be, and the per-track metadata.
package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/anytroops/landmark/internal/stoplist"
)

// Config is the JSON cluster file.
//
//	{
//	  "index_dir": "/data/index/p2_varint",
//	  "deadline_ms": 200,
//	  "t_abs": 30, "t_ratio": 1.0,
//	  "stop_fraction": 0.02,
//	  "partitions": [
//	    {"id": 0, "replicas": ["shard-0a:7001", "shard-0b:7001"]},
//	    {"id": 1, "replicas": ["shard-1a:7001", "shard-1b:7001"]}
//	  ]
//	}
type Config struct {
	IndexDir   string  `json:"index_dir"`
	DeadlineMs int     `json:"deadline_ms"`
	TAbs       uint32  `json:"t_abs"`
	TRatio     float64 `json:"t_ratio"`
	// StopFraction drops query landmarks whose hash has more postings, summed
	// over the whole index, than this fraction of the catalog's tracks (D30).
	// 0 disables it; otherwise the index needs a stop list (cmd/stoplist).
	StopFraction float64     `json:"stop_fraction"`
	Partitions   []Partition `json:"partitions"`
}

// Partition lists the replicas serving one partition.
type Partition struct {
	ID       uint32   `json:"id"`
	Replicas []string `json:"replicas"`
}

// Deadline is the end-to-end budget for one Identify.
func (c Config) Deadline() time.Duration { return time.Duration(c.DeadlineMs) * time.Millisecond }

// Track is one catalog entry (written by cmd/ingest).
type Track struct {
	Dense     uint32 `json:"dense"`
	FMAID     int    `json:"fma_id"`
	Partition uint32 `json:"partition"`
	Cluster   uint32 `json:"cluster"`
}

// Cluster is a validated Config joined with the index it serves.
type Cluster struct {
	Config
	// ExpectedCRC[p] is partition p's segment checksum from index.json.
	ExpectedCRC []uint32
	Catalog     []Track
	// Stop is the index's stop list when StopFraction > 0, else nil; query
	// landmarks whose hash count exceeds StopLimit are dropped.
	Stop      *stoplist.List
	StopLimit uint32
}

type indexManifest struct {
	NPartitions uint32 `json:"n_partitions"`
	NTracks     int    `json:"n_tracks"`
	Segments    []struct {
		Partition uint32 `json:"partition"`
		CRC32C    string `json:"crc32c"`
	} `json:"segments"`
	Stoplist *stoplist.ManifestEntry `json:"stoplist"`
}

// Load reads and validates the cluster file at path. A relative index_dir is
// resolved against the config file's directory.
func Load(path string) (*Cluster, error) {
	var cfg Config
	if err := readJSON(path, &cfg); err != nil {
		return nil, err
	}
	if cfg.IndexDir != "" && !filepath.IsAbs(cfg.IndexDir) {
		cfg.IndexDir = filepath.Join(filepath.Dir(path), cfg.IndexDir)
	}
	var man indexManifest
	if err := readJSON(filepath.Join(cfg.IndexDir, "index.json"), &man); err != nil {
		return nil, err
	}
	var catalog []Track
	if err := readJSON(filepath.Join(cfg.IndexDir, "catalog.json"), &catalog); err != nil {
		return nil, err
	}
	cl, err := build(cfg, man, catalog)
	if err != nil || cfg.StopFraction == 0 {
		return cl, err
	}
	if err := cl.loadStoplist(man); err != nil {
		return nil, fmt.Errorf("cluster config: %w", err)
	}
	return cl, nil
}

// loadStoplist loads the index's stop list and checks it belongs to this
// index: the checksum index.json records and the same track count.
func (cl *Cluster) loadStoplist(man indexManifest) error {
	l, err := stoplist.Load(filepath.Join(cl.IndexDir, man.Stoplist.File))
	if err != nil {
		return err
	}
	want, err := strconv.ParseUint(man.Stoplist.CRC32C, 16, 32)
	if err != nil {
		return fmt.Errorf("index.json: stoplist crc32c %q: %w", man.Stoplist.CRC32C, err)
	}
	if l.Checksum() != uint32(want) {
		return fmt.Errorf("stop list checksum %08x, index.json says %08x", l.Checksum(), want)
	}
	if int(l.NTracks()) != man.NTracks {
		return fmt.Errorf("stop list covers %d tracks, index has %d", l.NTracks(), man.NTracks)
	}
	if cl.StopLimit, err = l.Limit(cl.StopFraction); err != nil {
		return err
	}
	cl.Stop = l
	return nil
}

func build(cfg Config, man indexManifest, catalog []Track) (*Cluster, error) {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if cfg.DeadlineMs <= 0 {
		bad("deadline_ms must be positive")
	}
	if cfg.TRatio < 1 {
		bad("t_ratio must be >= 1")
	}
	switch {
	case cfg.StopFraction < 0 || cfg.StopFraction > 1:
		bad("stop_fraction %v must be in [0, 1]", cfg.StopFraction)
	case cfg.StopFraction > 0 && man.Stoplist == nil:
		bad("stop_fraction is set but index.json has no stoplist; build one with cmd/stoplist")
	}
	n := man.NPartitions
	if n == 0 || len(man.Segments) != int(n) {
		bad("index.json: %d segments for %d partitions", len(man.Segments), n)
	}
	if len(cfg.Partitions) != int(n) {
		bad("config lists %d partitions, index has %d", len(cfg.Partitions), n)
	}
	seen := map[uint32]bool{}
	for _, p := range cfg.Partitions {
		switch {
		case p.ID >= n:
			bad("partition %d out of range [0, %d)", p.ID, n)
		case seen[p.ID]:
			bad("partition %d listed twice", p.ID)
		case len(p.Replicas) == 0:
			bad("partition %d has no replicas", p.ID)
		}
		seen[p.ID] = true
	}
	crc := make([]uint32, n)
	for _, s := range man.Segments {
		if s.Partition >= n {
			bad("index.json: segment partition %d out of range", s.Partition)
			continue
		}
		v, err := strconv.ParseUint(s.CRC32C, 16, 32)
		if err != nil {
			bad("index.json: partition %d crc32c %q: %v", s.Partition, s.CRC32C, err)
		}
		crc[s.Partition] = uint32(v)
	}
	if len(catalog) != man.NTracks {
		bad("catalog has %d tracks, index.json says %d", len(catalog), man.NTracks)
	}
	for i, t := range catalog {
		if t.Dense != uint32(i) || (n > 0 && t.Partition != t.Dense%n) || int(t.Cluster) >= len(catalog) {
			bad("catalog entry %d is inconsistent: %+v", i, t)
			break
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("cluster config: %w", err)
	}
	// Order partitions by id so Partitions[p] is partition p.
	parts := make([]Partition, n)
	for _, p := range cfg.Partitions {
		parts[p.ID] = p
	}
	cfg.Partitions = parts
	return &Cluster{Config: cfg, ExpectedCRC: crc, Catalog: catalog}, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cluster config: %w", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("cluster config: parse %s: %w", path, err)
	}
	return nil
}
