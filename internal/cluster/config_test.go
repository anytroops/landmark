package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
