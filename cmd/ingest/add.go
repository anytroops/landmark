package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/anytroops/landmark/internal/audio"
	versioned "github.com/anytroops/landmark/internal/manifest"
	"github.com/anytroops/landmark/internal/segment"
)

// addSongs adds the files in listPath (name<TAB>path, as -extra) to the
// index in dir while it serves (D42): the next dense ids, one delta segment
// per partition that receives songs, the next version's catalog and
// extras, and last the next manifest, which publishes them all at once.
// The stop list stays as it was (D42 W3).
func addSongs(ctx context.Context, cfg config, dir, listPath string) error {
	start := time.Now()
	cur, err := versioned.Latest(dir)
	if err != nil {
		return err
	}
	triplets, err := indexKind(dir, cur)
	if err != nil {
		return err
	}
	cfg.triplets = triplets
	catalog, extras, err := readCatalog(dir, cur)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(listPath)
	if err != nil {
		return fmt.Errorf("read add list: %w", err)
	}
	added, err := parseExtras(string(b))
	if err != nil {
		return err
	}
	if len(added) == 0 {
		return errors.New("nothing to add")
	}
	nextFMA := -1 // extras are -1, -2, ...: continue below the lowest
	for _, e := range extras {
		nextFMA = min(nextFMA, e.FMAID-1)
	}

	ids := make([]int, len(added))
	for k := range added {
		ids[k] = k
	}
	lms := make([][]segment.Entry, len(added))
	for r := range fingerprintAll(ctx, cfg, func(k int) string { return added[k].Path }, ids) {
		if r.err != nil {
			if errors.Is(r.err, audio.ErrDecode) {
				slog.Warn("decode failed, skipping", "name", added[r.dense].Name, "err", r.err)
				continue
			}
			return r.err
		}
		lms[r.dense] = r.lms
	}

	// Dense ids for the songs that decoded, in list order.
	nParts := cur.NPartitions
	parts := make([][]segment.Entry, nParts)
	dense := cur.NTracks
	for k, e := range added {
		if lms[k] == nil {
			continue
		}
		p := dense % nParts
		for _, l := range lms[k] {
			l.Track = dense / nParts
			parts[p] = append(parts[p], l)
		}
		e.Dense, e.FMAID = dense, nextFMA
		nextFMA--
		extras = append(extras, e)
		catalog = append(catalog, CatalogTrack{Dense: dense, FMAID: e.FMAID, Partition: p, Cluster: dense})
		dense++
	}
	if dense == cur.NTracks {
		return errors.New("no song could be decoded")
	}

	next := versioned.Manifest{Version: cur.Version + 1, NPartitions: nParts, NTracks: dense,
		Partitions: make([][]versioned.Member, nParts),
		Catalog:    fmt.Sprintf("catalog-%08d.json", cur.Version+1), Extras: fmt.Sprintf("extras-%08d.json", cur.Version+1)}
	for p := range nParts {
		next.Partitions[p] = append([]versioned.Member(nil), cur.Partitions[p]...)
		if len(parts[p]) == 0 {
			continue
		}
		name := fmt.Sprintf("part-%03d.v%08d.lmrk", p, next.Version)
		meta := segment.Meta{Partition: p, NPartitions: nParts, NTracks: (dense + nParts - 1 - p) / nParts,
			Encoding: cfg.encoding, Aux: cfg.triplets}
		mem, err := writeMember(dir, name, meta, parts[p])
		if err != nil {
			return err
		}
		next.Partitions[p] = append(next.Partitions[p], mem)
	}
	if err := publish(dir, next, catalog, extras); err != nil {
		return err
	}
	slog.Info("added songs", "dir", dir, "version", next.Version, "added", int(dense-cur.NTracks),
		"failed", len(added)-int(dense-cur.NTracks), "tracks", dense, "wall_s", fmt.Sprintf("%.1f", time.Since(start).Seconds()))
	return nil
}

// allEntries reads every posting of a set back as segment entries.
func allEntries(set *segment.Set) ([]segment.Entry, error) {
	var out []segment.Entry
	var aux []segment.AuxPosting
	var plain []segment.Posting
	var err error
	for _, seg := range set.Segments() {
		for i := range seg.NKeys() {
			h := seg.Key(i)
			if set.HasAux() {
				if aux, err = seg.AppendAuxPostings(aux[:0], h); err != nil {
					return nil, err
				}
				for _, p := range aux {
					out = append(out, segment.Entry{Hash: h, Track: p.Track, T: p.T, Span: p.Span, Band: p.Band})
				}
				continue
			}
			if plain, err = seg.AppendPostings(plain[:0], h); err != nil {
				return nil, err
			}
			for _, p := range plain {
				out = append(out, segment.Entry{Hash: h, Track: p.Track, T: p.T})
			}
		}
	}
	return out, nil
}

// writeMember writes one segment and returns its manifest entry.
func writeMember(dir, name string, meta segment.Meta, entries []segment.Entry) (versioned.Member, error) {
	path := filepath.Join(dir, name)
	if err := segment.Write(path, meta, entries); err != nil {
		return versioned.Member{}, err
	}
	seg, err := segment.Open(path)
	if err != nil {
		return versioned.Member{}, err
	}
	defer func() { _ = seg.Close() }()
	return versioned.Member{File: name, CRC32C: versioned.FormatCRC(seg.Checksum()), NTracks: meta.NTracks}, nil
}

// publish writes the version's catalog and extras durably, then its
// manifest: until the manifest exists nothing refers to the new files.
func publish(dir string, m versioned.Manifest, catalog []CatalogTrack, extras []Extra) error {
	if err := writeDurable(filepath.Join(dir, m.Catalog), catalog); err != nil {
		return err
	}
	if err := writeDurable(filepath.Join(dir, m.Extras), extras); err != nil {
		return err
	}
	return versioned.Write(dir, m)
}

func writeDurable(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// readCatalog reads a version's catalog and extras.
func readCatalog(dir string, m versioned.Manifest) ([]CatalogTrack, []Extra, error) {
	var catalog []CatalogTrack
	b, err := os.ReadFile(filepath.Join(dir, m.Catalog))
	if err != nil {
		return nil, nil, fmt.Errorf("read catalog: %w", err)
	}
	if err := json.Unmarshal(b, &catalog); err != nil {
		return nil, nil, fmt.Errorf("parse catalog: %w", err)
	}
	var extras []Extra
	if m.Extras != "" {
		if b, err = os.ReadFile(filepath.Join(dir, m.Extras)); err != nil {
			return nil, nil, fmt.Errorf("read extras: %w", err)
		}
		if err := json.Unmarshal(b, &extras); err != nil {
			return nil, nil, fmt.Errorf("parse extras: %w", err)
		}
	}
	if uint32(len(catalog)) != m.NTracks {
		return nil, nil, fmt.Errorf("catalog has %d tracks, manifest %d", len(catalog), m.NTracks)
	}
	return catalog, extras, nil
}

// indexKind reports whether the index holds triplet landmarks.
func indexKind(dir string, m versioned.Manifest) (bool, error) {
	seg, err := segment.Open(filepath.Join(dir, m.Partitions[0][0].File))
	if err != nil {
		return false, err
	}
	defer func() { _ = seg.Close() }()
	return seg.HasAux(), nil
}
