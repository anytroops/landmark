// Package catalog reads an index's catalog (written by cmd/ingest): each
// dense track id's FMA id and duplicate cluster, plus display names of
// non-FMA tracks. An index with songs added while serving (D42) has one
// catalog per version, named by its manifest; Load reads the newest.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anytroops/landmark/internal/manifest"
)

// Track is one entry of catalog.json.
type Track struct {
	Dense   uint32 `json:"dense"`
	FMAID   int    `json:"fma_id"`
	Cluster uint32 `json:"cluster"`
}

// Catalog names and groups an index's tracks.
type Catalog struct {
	Tracks []Track
	extras map[int]string // FMA id -> name, for tracks not from FMA
	paths  map[int]string // FMA id -> audio file, for tracks not from FMA
}

// files names the newest version's catalog and extras in dir (catalog.json
// and extras.json for an index without manifests).
func files(dir string) (catalogFile, extrasFile string) {
	if m, err := manifest.Latest(dir); err == nil && m.Catalog != "" {
		return m.Catalog, m.Extras
	}
	return "catalog.json", "extras.json"
}

// Load reads the newest version's catalog in dir.
func Load(dir string) (*Catalog, error) {
	catalogFile, extrasFile := files(dir)
	tracks, err := readTracksFile(filepath.Join(dir, catalogFile))
	if err != nil {
		return nil, err
	}
	c := &Catalog{Tracks: tracks, extras: map[int]string{}, paths: map[int]string{}}
	if extrasFile == "" {
		return c, nil
	}
	if b, err := os.ReadFile(filepath.Join(dir, extrasFile)); err == nil {
		var ex []struct {
			FMAID int    `json:"fma_id"`
			Name  string `json:"name"`
			Path  string `json:"path"`
		}
		if err := json.Unmarshal(b, &ex); err != nil {
			return nil, fmt.Errorf("parse %s extras: %w", dir, err)
		}
		for _, e := range ex {
			c.extras[e.FMAID] = e.Name
			if e.Path != "" {
				c.paths[e.FMAID] = e.Path
			}
		}
	}
	return c, nil
}

func readTracks(dir string) ([]Track, error) {
	catalogFile, _ := files(dir)
	return readTracksFile(filepath.Join(dir, catalogFile))
}

func readTracksFile(path string) ([]Track, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	var tracks []Track
	if err := json.Unmarshal(b, &tracks); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return tracks, nil
}

// Name is a display name for a dense id.
func (c *Catalog) Name(dense int) string {
	if dense < 0 || dense >= len(c.Tracks) {
		return fmt.Sprintf("track %d", dense)
	}
	if name, ok := c.extras[c.Tracks[dense].FMAID]; ok {
		return name
	}
	return fmt.Sprintf("FMA %06d", c.Tracks[dense].FMAID)
}

// Audio returns a track's audio file: the path recorded for a non-FMA
// track, or fmaDir/NNN/NNNNNN.mp3 for an FMA track (if fmaDir is set).
func (c *Catalog) Audio(dense int, fmaDir string) (string, bool) {
	if dense < 0 || dense >= len(c.Tracks) {
		return "", false
	}
	id := c.Tracks[dense].FMAID
	if p, ok := c.paths[id]; ok {
		return p, true
	}
	if id < 0 || fmaDir == "" {
		return "", false
	}
	tid := fmt.Sprintf("%06d", id)
	return filepath.Join(fmaDir, tid[:3], tid+".mp3"), true
}

// Cluster is a dense id's duplicate cluster (D12); ids outside the catalog
// are their own cluster.
func (c *Catalog) Cluster(dense uint32) uint32 {
	if int(dense) < len(c.Tracks) {
		return c.Tracks[dense].Cluster
	}
	return dense
}

// Same checks that two indexes' newest catalogs number the same tracks the
// same way, so their answers can be combined.
func Same(a, b string) error {
	ca, err := readTracks(a)
	if err != nil {
		return err
	}
	cb, err := readTracks(b)
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
