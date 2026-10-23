package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "catalog.json", `[{"dense":0,"fma_id":2,"cluster":0},{"dense":1,"fma_id":900001,"cluster":0}]`)
	write(t, dir, "extras.json", `[{"fma_id":900001,"name":"Some Artist - Song","path":"/audio/song.mp3"}]`)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		dense int
		name  string
	}{{0, "FMA 000002"}, {1, "Some Artist - Song"}, {5, "track 5"}, {-1, "track -1"}}
	for _, tt := range tests {
		if got := c.Name(tt.dense); got != tt.name {
			t.Errorf("Name(%d) = %q, want %q", tt.dense, got, tt.name)
		}
	}
	if c.Cluster(1) != 0 || c.Cluster(9) != 9 {
		t.Errorf("clusters %d %d", c.Cluster(1), c.Cluster(9))
	}
	audio := []struct {
		dense int
		path  string
		ok    bool
	}{{0, "/fma/000/000002.mp3", true}, {1, "/audio/song.mp3", true}, {7, "", false}}
	for _, a := range audio {
		if p, ok := c.Audio(a.dense, "/fma"); p != a.path || ok != a.ok {
			t.Errorf("Audio(%d) = %q %v, want %q %v", a.dense, p, ok, a.path, a.ok)
		}
	}
}

func TestSame(t *testing.T) {
	a, b, c := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, a, "catalog.json", `[{"dense":0,"fma_id":2},{"dense":1,"fma_id":5}]`)
	write(t, b, "catalog.json", `[{"dense":0,"fma_id":2},{"dense":1,"fma_id":5}]`)
	write(t, c, "catalog.json", `[{"dense":0,"fma_id":5},{"dense":1,"fma_id":2}]`)
	if err := Same(a, b); err != nil {
		t.Errorf("same catalogs: %v", err)
	}
	if err := Same(a, c); err == nil {
		t.Error("reordered catalog: want an error")
	}
	if err := Same(a, t.TempDir()); err == nil {
		t.Error("missing catalog: want an error")
	}
}

func TestLoadReadsTheNewestVersion(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "catalog.json", `[{"dense":0,"fma_id":2,"cluster":0}]`)
	write(t, dir, "catalog-00000002.json", `[{"dense":0,"fma_id":2,"cluster":0},{"dense":1,"fma_id":-1,"cluster":1}]`)
	write(t, dir, "extras-00000002.json", `[{"fma_id":-1,"name":"Added Song"}]`)
	write(t, dir, "manifest-00000002.json", `{"version":2,"n_partitions":1,"n_tracks":2,"partitions":[[]],`+
		`"catalog":"catalog-00000002.json","extras":"extras-00000002.json"}`)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Tracks) != 2 || c.Name(1) != "Added Song" {
		t.Fatalf("tracks %d, name %q", len(c.Tracks), c.Name(1))
	}
}
