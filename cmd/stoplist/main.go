// Command stoplist adds a stop list (D30) to an existing index directory:
// it counts every hash's postings across all of the index's segments,
// writes stoplist.lmst with the hashes at or above the minimum, and records
// the file and its checksum in index.json. cmd/ingest does the same for new
// indexes.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/anytroops/landmark/internal/segment"
	"github.com/anytroops/landmark/internal/stoplist"
)

func main() {
	dir := flag.String("index", "", "index directory with index.json (required)")
	minFraction := flag.Float64("min-fraction", stoplist.DefaultMinFraction,
		"list hashes with at least this fraction of the catalog's tracks in postings; the smallest usable stop_fraction")
	flag.Parse()
	if *dir == "" {
		log.Fatal("-index is required")
	}
	if err := run(*dir, *minFraction); err != nil {
		log.Fatal(err)
	}
}

func run(dir string, minFraction float64) error {
	manPath := filepath.Join(dir, "index.json")
	b, err := os.ReadFile(manPath)
	if err != nil {
		return err
	}
	// Decode into raw fields so everything else in index.json is kept as is.
	var man map[string]json.RawMessage
	if err := json.Unmarshal(b, &man); err != nil {
		return fmt.Errorf("parse %s: %w", manPath, err)
	}
	var segs []struct {
		File string `json:"file"`
	}
	if err := json.Unmarshal(man["segments"], &segs); err != nil {
		return fmt.Errorf("%s segments: %w", manPath, err)
	}
	var nTracks uint32
	if err := json.Unmarshal(man["n_tracks"], &nTracks); err != nil {
		return fmt.Errorf("%s n_tracks: %w", manPath, err)
	}

	opened := make([]*segment.Segment, 0, len(segs))
	defer func() {
		for _, s := range opened {
			_ = s.Close()
		}
	}()
	for _, s := range segs {
		seg, err := segment.Open(filepath.Join(dir, s.File))
		if err != nil {
			return err
		}
		opened = append(opened, seg)
	}
	l, err := stoplist.FromSegments(opened, stoplist.MinCountFor(nTracks, minFraction))
	if err != nil {
		return err
	}
	if l.NTracks() != nTracks {
		return fmt.Errorf("segments hold %d tracks, index.json says %d", l.NTracks(), nTracks)
	}
	if err := l.Write(filepath.Join(dir, stoplist.FileName)); err != nil {
		return err
	}

	entry, err := json.Marshal(stoplist.ManifestEntry{File: stoplist.FileName, CRC32C: fmt.Sprintf("%08x", l.Checksum())})
	if err != nil {
		return err
	}
	man["stoplist"] = entry
	out, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	tmp := manPath + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, manPath); err != nil {
		return err
	}
	log.Printf("%s: %d hashes with >= %d postings over %d tracks, crc32c %08x",
		dir, l.Len(), l.MinCount(), l.NTracks(), l.Checksum())
	return nil
}
