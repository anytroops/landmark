package manifest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anytroops/landmark/internal/segment"
)

// writeSeg writes a one-posting segment and returns its member entry.
func writeSeg(t *testing.T, dir, name string, meta segment.Meta, track uint32) Member {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := segment.Write(path, meta, []segment.Entry{{Hash: 7, Track: track, T: 3}}); err != nil {
		t.Fatal(err)
	}
	s, err := segment.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	return Member{File: name, CRC32C: FormatCRC(s.Checksum()), NTracks: meta.NTracks}
}

// legacyIndex writes a 2-partition, 5-track index as ingest did before
// manifests: partition 0 holds ids 0, 2, 4 (local 0-2), partition 1 ids 1, 3.
func legacyIndex(t *testing.T) (string, []Member) {
	t.Helper()
	dir := t.TempDir()
	m0 := writeSeg(t, dir, "part-000.lmrk", segment.Meta{Partition: 0, NPartitions: 2, NTracks: 3}, 2)
	m1 := writeSeg(t, dir, "part-001.lmrk", segment.Meta{Partition: 1, NPartitions: 2, NTracks: 2}, 1)
	idx := map[string]any{"n_partitions": 2, "n_tracks": 5, "segments": []map[string]any{
		{"partition": 0, "file": m0.File, "crc32c": m0.CRC32C},
		{"partition": 1, "file": m1.File, "crc32c": m1.CRC32C},
	}}
	b, _ := json.Marshal(idx)
	if err := os.WriteFile(filepath.Join(dir, "index.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, []Member{m0, m1}
}

func TestLegacyIndexIsVersionOne(t *testing.T) {
	dir, members := legacyIndex(t)
	m, err := Latest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 1 || m.NTracks != 5 || m.Catalog != "catalog.json" || len(m.Partitions) != 2 ||
		m.Partitions[0][0] != members[0] || m.Partitions[1][0] != members[1] {
		t.Fatalf("version 1 = %+v", m)
	}
	set, err := Open(dir, m, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = set.Close() }()
	if c, _ := m.Checksum(1); c != set.Segments()[0].Checksum() {
		t.Errorf("single-member checksum %08x, segment %08x", c, set.Segments()[0].Checksum())
	}
}

func TestPublishAndOpenADelta(t *testing.T) {
	dir, members := legacyIndex(t)
	v1, err := Latest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Add global ids 5 and 6: local 2 in partition 1, local 3 in partition 0.
	d0 := writeSeg(t, dir, "part-000.d00000002.lmrk", segment.Meta{Partition: 0, NPartitions: 2, NTracks: 4}, 3)
	d1 := writeSeg(t, dir, "part-001.d00000002.lmrk", segment.Meta{Partition: 1, NPartitions: 2, NTracks: 3}, 2)
	v2 := v1
	v2.Version, v2.NTracks = 2, 7
	v2.Partitions = [][]Member{{members[0], d0}, {members[1], d1}}
	if err := Write(dir, v2); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, v2); err == nil {
		t.Error("publishing version 2 twice: want an error")
	}
	got, err := Latest(dir)
	if err != nil || got.Version != 2 || len(got.Partitions[0]) != 2 {
		t.Fatalf("latest after publishing: %+v, %v", got, err)
	}
	set, err := Open(dir, got, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = set.Close() }()
	ps, err := set.AppendPostings(nil, 7)
	if err != nil || len(ps) != 2 || set.GlobalTrack(ps[1].Track) != 6 {
		t.Fatalf("postings %+v (%v), want base and delta, the delta's being global id 6", ps, err)
	}
	c1, _ := v1.Checksum(0)
	c2, _ := got.Checksum(0)
	if c1 == c2 {
		t.Error("adding a delta did not change the partition checksum")
	}
	if v1again, err := Read(dir, 1); err == nil {
		t.Errorf("version 1 was never written as a file, read %+v", v1again)
	}
}

func TestOpenRejectsAChangedFile(t *testing.T) {
	dir, members := legacyIndex(t)
	m, err := Latest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Partitions[0][0].CRC32C = FormatCRC(0xdeadbeef)
	if _, err := Open(dir, m, 0); !errors.Is(err, ErrMismatch) {
		t.Errorf("listed crc differs: %v, want ErrMismatch", err)
	}
	m.Partitions[0][0] = members[1] // partition 1's file listed for partition 0
	if _, err := Open(dir, m, 0); !errors.Is(err, ErrMismatch) {
		t.Errorf("other partition's file: %v, want ErrMismatch", err)
	}
}
