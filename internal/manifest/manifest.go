// Package manifest versions an index's contents so songs can be added while
// it serves (D42). Version v of an index is the file manifest-<v>.json in
// the index directory: for every partition, its base segment and delta
// segments with their CRC-32C and track ranges, and the catalog files that
// name the version's tracks. Manifests are written atomically and never
// changed, so a reader holding version v keeps a consistent view while
// version v+1 is published beside it.
//
// An index built before manifests existed (index.json and catalog.json
// only) reads as version 1.
package manifest

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/anytroops/landmark/internal/segment"
)

// Member is one segment file of a partition.
type Member struct {
	File    string `json:"file"`
	CRC32C  string `json:"crc32c"` // hex, as index.json writes it
	NTracks uint32 `json:"n_tracks"`
}

// Manifest is one version of an index.
type Manifest struct {
	Version     uint64     `json:"version"`
	NPartitions uint32     `json:"n_partitions"`
	NTracks     uint32     `json:"n_tracks"` // global ids in use
	Partitions  [][]Member `json:"partitions"`
	Catalog     string     `json:"catalog"`
	Extras      string     `json:"extras,omitempty"`
}

var fileName = regexp.MustCompile(`^manifest-(\d{8})\.json$`)

// Name is the file name of version v.
func Name(v uint64) string { return fmt.Sprintf("manifest-%08d.json", v) }

// Latest returns the newest version in dir, or version 1 synthesized from
// index.json if dir has no manifest.
func Latest(dir string) (Manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest: %w", err)
	}
	var best uint64
	for _, e := range entries {
		if m := fileName.FindStringSubmatch(e.Name()); m != nil {
			if v, _ := strconv.ParseUint(m[1], 10, 64); v > best {
				best = v
			}
		}
	}
	if best == 0 {
		return fromIndexJSON(dir)
	}
	return Read(dir, best)
}

// Read returns version v.
func Read(dir string, v uint64) (Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, Name(v)))
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("manifest: parse %s: %w", Name(v), err)
	}
	if m.Version != v || int(m.NPartitions) != len(m.Partitions) {
		return Manifest{}, fmt.Errorf("manifest: %s: version %d, %d partitions listed of %d", Name(v), m.Version, len(m.Partitions), m.NPartitions)
	}
	return m, nil
}

// fromIndexJSON reads a pre-manifest index as version 1.
func fromIndexJSON(dir string) (Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest: no manifest and no index.json: %w", err)
	}
	var idx struct {
		NPartitions uint32 `json:"n_partitions"`
		NTracks     uint32 `json:"n_tracks"`
		Segments    []struct {
			Partition int    `json:"partition"`
			File      string `json:"file"`
			CRC32C    string `json:"crc32c"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(b, &idx); err != nil {
		return Manifest{}, fmt.Errorf("manifest: parse index.json: %w", err)
	}
	m := Manifest{Version: 1, NPartitions: idx.NPartitions, NTracks: idx.NTracks,
		Partitions: make([][]Member, idx.NPartitions), Catalog: "catalog.json"}
	if _, err := os.Stat(filepath.Join(dir, "extras.json")); err == nil {
		m.Extras = "extras.json"
	}
	for _, s := range idx.Segments {
		if s.Partition < 0 || s.Partition >= int(idx.NPartitions) {
			return Manifest{}, fmt.Errorf("manifest: index.json lists partition %d of %d", s.Partition, idx.NPartitions)
		}
		p := uint32(s.Partition)
		local := (idx.NTracks + idx.NPartitions - 1 - p) / idx.NPartitions // ids g < n with g mod P == p
		m.Partitions[p] = []Member{{File: s.File, CRC32C: s.CRC32C, NTracks: local}}
	}
	for p, ms := range m.Partitions {
		if len(ms) == 0 {
			return Manifest{}, fmt.Errorf("manifest: index.json has no segment for partition %d", p)
		}
	}
	return m, nil
}

// Write publishes m as dir/manifest-<version>.json: temp file, fsync,
// rename, fsync the directory. Publishing an existing version is an error,
// so a version never changes once readers may hold it.
func Write(dir string, m Manifest) error {
	path := filepath.Join(dir, Name(m.Version))
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("manifest: version %d exists", m.Version)
	}
	b, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	_, err = f.Write(append(b, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("manifest: write %s: %w", Name(m.Version), err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// Checksum identifies partition p's content in this version (D26): the
// segment CRC for a single member (as before manifests), else CRC-32C over
// the members' CRCs in order.
func (m Manifest) Checksum(p uint32) (uint32, error) {
	members := m.Partitions[p]
	crcs := make([]byte, 0, 4*len(members))
	var first uint32
	for i, mem := range members {
		c, err := parseCRC(mem.CRC32C)
		if err != nil {
			return 0, err
		}
		if i == 0 {
			first = c
		}
		crcs = binary.LittleEndian.AppendUint32(crcs, c)
	}
	if len(members) == 1 {
		return first, nil
	}
	return crc32.Checksum(crcs, crc32.MakeTable(crc32.Castagnoli)), nil
}

func parseCRC(s string) (uint32, error) {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("manifest: bad crc32c %q", s)
	}
	return uint32(v), nil
}

// FormatCRC writes a CRC as manifests and index.json do.
func FormatCRC(c uint32) string { return fmt.Sprintf("%08x", c) }

// ErrMismatch marks a segment file whose content differs from its manifest.
var ErrMismatch = errors.New("manifest: segment differs from manifest")

// Open opens partition p of version m: every member, checked against its
// listed CRC and track range, as one segment.Set.
func Open(dir string, m Manifest, p uint32) (*segment.Set, error) {
	if p >= m.NPartitions {
		return nil, fmt.Errorf("manifest: partition %d of %d", p, m.NPartitions)
	}
	var segs []*segment.Segment
	fail := func(err error) (*segment.Set, error) {
		for _, s := range segs {
			_ = s.Close()
		}
		return nil, err
	}
	for _, mem := range m.Partitions[p] {
		want, err := parseCRC(mem.CRC32C)
		if err != nil {
			return fail(err)
		}
		s, err := segment.Open(filepath.Join(dir, mem.File))
		if err != nil {
			return fail(fmt.Errorf("manifest: %w", err))
		}
		segs = append(segs, s)
		if s.Checksum() != want || s.NTracks() != mem.NTracks {
			return fail(fmt.Errorf("%w: %s has crc %08x and %d tracks, manifest %08x and %d",
				ErrMismatch, mem.File, s.Checksum(), s.NTracks(), want, mem.NTracks))
		}
		if part, n := s.Partition(); part != p || n != m.NPartitions {
			return fail(fmt.Errorf("%w: %s is partition %d of %d", ErrMismatch, mem.File, part, n))
		}
	}
	set, err := segment.NewSet(segs)
	if err != nil {
		return fail(err)
	}
	return set, nil
}
