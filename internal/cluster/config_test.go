package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anytroops/landmark/internal/stoplist"
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// fixture writes a valid 2-partition cluster and lets mutate change it.
func fixture(t *testing.T, mutate func(cfg map[string]any, man map[string]any, cat []map[string]any) []map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	idx := filepath.Join(dir, "index")
	if err := os.Mkdir(idx, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{
		"index_dir": "index", "deadline_ms": 200, "t_abs": 30, "t_ratio": 1.0,
		"partitions": []map[string]any{
			{"id": 1, "replicas": []string{"b1:1", "b2:1"}},
			{"id": 0, "replicas": []string{"a1:1"}},
		},
	}
	man := map[string]any{"n_partitions": 2, "n_tracks": 3, "segments": []map[string]any{
		{"partition": 0, "crc32c": "0000abcd"}, {"partition": 1, "crc32c": "deadbeef"},
	}}
	cat := []map[string]any{
		{"dense": 0, "fma_id": 10, "partition": 0, "cluster": 0},
		{"dense": 1, "fma_id": 20, "partition": 1, "cluster": 1},
		{"dense": 2, "fma_id": 30, "partition": 0, "cluster": 0},
	}
	if mutate != nil {
		cat = mutate(cfg, man, cat)
	}
	writeJSON(t, filepath.Join(dir, "cluster.json"), cfg)
	writeJSON(t, filepath.Join(idx, "index.json"), man)
	writeJSON(t, filepath.Join(idx, "catalog.json"), cat)
	return filepath.Join(dir, "cluster.json")
}

func TestLoadValid(t *testing.T) {
	c, err := Load(fixture(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Partitions[0].ID != 0 || c.Partitions[1].Replicas[1] != "b2:1" {
		t.Errorf("partitions not ordered by id: %+v", c.Partitions)
	}
	if c.ExpectedCRC[0] != 0xabcd || c.ExpectedCRC[1] != 0xdeadbeef {
		t.Errorf("ExpectedCRC = %x", c.ExpectedCRC)
	}
	if c.Deadline().Milliseconds() != 200 || c.Catalog[2].Cluster != 0 {
		t.Errorf("deadline or catalog wrong: %+v", c)
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(cfg, man map[string]any, cat []map[string]any) []map[string]any
		wantErr string
	}{
		{"missing partition", func(cfg, _ map[string]any, cat []map[string]any) []map[string]any {
			cfg["partitions"] = []map[string]any{{"id": 0, "replicas": []string{"a:1"}}}
			return cat
		}, "lists 1 partitions"},
		{"duplicate partition", func(cfg, _ map[string]any, cat []map[string]any) []map[string]any {
			cfg["partitions"] = []map[string]any{{"id": 0, "replicas": []string{"a:1"}}, {"id": 0, "replicas": []string{"b:1"}}}
			return cat
		}, "listed twice"},
		{"no replicas", func(cfg, _ map[string]any, cat []map[string]any) []map[string]any {
			cfg["partitions"] = []map[string]any{{"id": 0, "replicas": []string{}}, {"id": 1, "replicas": []string{"b:1"}}}
			return cat
		}, "no replicas"},
		{"bad crc", func(_, man map[string]any, cat []map[string]any) []map[string]any {
			man["segments"] = []map[string]any{{"partition": 0, "crc32c": "zz"}, {"partition": 1, "crc32c": "1"}}
			return cat
		}, "crc32c"},
		{"catalog size", func(_, _ map[string]any, cat []map[string]any) []map[string]any { return cat[:2] }, "catalog has 2"},
		{"catalog partition", func(_, _ map[string]any, cat []map[string]any) []map[string]any {
			cat[1]["partition"] = 0
			return cat
		}, "inconsistent"},
		{"zero deadline", func(cfg, _ map[string]any, cat []map[string]any) []map[string]any {
			cfg["deadline_ms"] = 0
			return cat
		}, "deadline_ms"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(fixture(t, tc.mutate))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// withStoplist writes l into the fixture's index and records it in
// index.json with crc (the list's own checksum when empty).
func withStoplist(t *testing.T, cfgPath string, l *stoplist.List, crc string) {
	t.Helper()
	idx := filepath.Join(filepath.Dir(cfgPath), "index")
	if err := l.Write(filepath.Join(idx, stoplist.FileName)); err != nil {
		t.Fatal(err)
	}
	if crc == "" {
		crc = fmt.Sprintf("%08x", l.Checksum())
	}
	var man map[string]any
	b, err := os.ReadFile(filepath.Join(idx, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &man); err != nil {
		t.Fatal(err)
	}
	man["stoplist"] = stoplist.ManifestEntry{File: stoplist.FileName, CRC32C: crc}
	writeJSON(t, filepath.Join(idx, "index.json"), man)
}

func stopFraction(f float64) func(cfg, _ map[string]any, cat []map[string]any) []map[string]any {
	return func(cfg, _ map[string]any, cat []map[string]any) []map[string]any {
		cfg["stop_fraction"] = f
		return cat
	}
}

func TestLoadStoplist(t *testing.T) {
	l, err := stoplist.New(3, 1, []uint32{5, 9}, []uint32{1, 4})
	if err != nil {
		t.Fatal(err)
	}
	path := fixture(t, stopFraction(0.7)) // 0.7 x 3 tracks: keep hashes with <= 2 postings
	withStoplist(t, path, l, "")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Stop == nil || c.StopLimit != 2 || c.Stop.Count(9) != 4 {
		t.Fatalf("Stop = %v, StopLimit = %d, want the list and 2", c.Stop, c.StopLimit)
	}

	// Without stop_fraction the list is ignored even when present.
	path = fixture(t, nil)
	withStoplist(t, path, l, "")
	if c, err := Load(path); err != nil || c.Stop != nil {
		t.Fatalf("Load = %v, %v; want no stop list", c, err)
	}
}

func TestLoadStoplistRejects(t *testing.T) {
	good, err := stoplist.New(3, 1, []uint32{5}, []uint32{2})
	if err != nil {
		t.Fatal(err)
	}
	otherIndex, err := stoplist.New(4, 1, []uint32{5}, []uint32{2})
	if err != nil {
		t.Fatal(err)
	}
	highMin, err := stoplist.New(3, 2, []uint32{5}, []uint32{2})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		fraction float64
		list     *stoplist.List // nil: index has no stop list
		crc      string
		wantErr  string
	}{
		{"negative fraction", -0.1, good, "", "stop_fraction"},
		{"fraction above 1", 1.5, good, "", "stop_fraction"},
		{"no stop list in index", 0.5, nil, "", "no stoplist"},
		{"checksum mismatch", 0.5, good, "00000001", "checksum"},
		{"different index", 0.5, otherIndex, "", "covers 4 tracks"},
		{"cutoff below the list's minimum", 0.5, highMin, "", "below the list's minimum"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := fixture(t, stopFraction(tc.fraction))
			if tc.list != nil {
				withStoplist(t, path, tc.list, tc.crc)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}
