// Package catalog reads an index's catalog.json (written by cmd/ingest):
// each dense track id's FMA id and duplicate cluster, plus display names of
// non-FMA tracks from extras.json beside it.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
}

// Load reads dir/catalog.json and, if present, dir/extras.json.
func Load(dir string) (*Catalog, error) {
	tracks, err := readTracks(dir)
	if err != nil {
		return nil, err
	}
	c := &Catalog{Tracks: tracks, extras: map[int]string{}}
	if b, err := os.ReadFile(filepath.Join(dir, "extras.json")); err == nil {
		var ex []struct {
			FMAID int    `json:"fma_id"`
			Name  string `json:"name"`
		}
		if err := json.Unmarshal(b, &ex); err != nil {
			return nil, fmt.Errorf("parse %s extras: %w", dir, err)
		}
		for _, e := range ex {
			c.extras[e.FMAID] = e.Name
		}
	}
	return c, nil
}

func readTracks(dir string) ([]Track, error) {
	b, err := os.ReadFile(filepath.Join(dir, "catalog.json"))
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	var tracks []Track
	if err := json.Unmarshal(b, &tracks); err != nil {
		return nil, fmt.Errorf("parse %s catalog: %w", dir, err)
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

// Cluster is a dense id's duplicate cluster (D12); ids outside the catalog
// are their own cluster.
func (c *Catalog) Cluster(dense uint32) uint32 {
	if int(dense) < len(c.Tracks) {
		return c.Tracks[dense].Cluster
	}
	return dense
}

// Same checks that two indexes number the same tracks the same way, so
// their answers can be combined.
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
