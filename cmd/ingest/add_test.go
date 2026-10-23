package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/fingerprint"
	versioned "github.com/anytroops/landmark/internal/manifest"
	"github.com/anytroops/landmark/internal/match"
	"github.com/anytroops/landmark/internal/segment"
)

// songs writes n distinct 12 s noise recordings and returns their paths.
func songs(t *testing.T, n int) []string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	var out []string
	for i := range n {
		path := filepath.Join(dir, fmt.Sprintf("song%d.wav", i))
		src := fmt.Sprintf("anoisesrc=seed=%d:color=pink:sample_rate=22050:duration=12", 11+i)
		if b, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", src, path).CombinedOutput(); err != nil {
			t.Fatalf("make song: %v: %s", err, b)
		}
		out = append(out, path)
	}
	return out
}

func list(t *testing.T, paths []string) string {
	t.Helper()
	var b strings.Builder
	for _, p := range paths {
		fmt.Fprintf(&b, "%s\t%s\n", filepath.Base(p), p)
	}
	path := filepath.Join(t.TempDir(), "list.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// identify searches every partition of version v for the song at path and
// returns the best global track id.
func identify(t *testing.T, dir string, v uint64, path string) (uint32, uint32) {
	t.Helper()
	m, err := versioned.Get(dir, v)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := audio.Decode(context.Background(), path, audio.SampleRate)
	if err != nil {
		t.Fatal(err)
	}
	query := fingerprint.New(fingerprint.DefaultParams()).FingerprintQuery(pcm[2*audio.SampleRate:8*audio.SampleRate], 4)
	var best, score uint32
	for p := range m.NPartitions {
		set, err := versioned.Open(dir, m, p)
		if err != nil {
			t.Fatal(err)
		}
		var mt match.Matcher
		res, err := mt.Search(set, query, 5)
		_ = set.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Candidates) > 0 && res.Candidates[0].Score > score {
			best, score = set.GlobalTrack(res.Candidates[0].Track), res.Candidates[0].Score
		}
	}
	return best, score
}

func TestAddSongsThenMerge(t *testing.T) {
	files := songs(t, 4)
	dir := t.TempDir()
	cfg := config{out: dir, extra: list(t, files[:2]), partitions: []int{2}, encoding: segment.EncodingVarint, workers: 2}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := addSongs(context.Background(), cfg, dir, list(t, files[2:])); err != nil {
		t.Fatal(err)
	}
	v2, err := versioned.Read(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if v2.NTracks != 4 || len(v2.Partitions[0]) != 2 || len(v2.Partitions[1]) != 2 {
		t.Fatalf("version 2 = %+v; want 4 tracks, a delta in each partition", v2)
	}
	catalog, extras, err := readCatalog(dir, v2)
	if err != nil || len(catalog) != 4 || len(extras) != 4 || extras[3].FMAID != -4 || extras[3].Dense != 3 {
		t.Fatalf("version 2 catalog %+v extras %+v (%v)", catalog, extras, err)
	}
	scores := map[uint32]uint32{}
	for want, f := range files {
		got, score := identify(t, dir, 2, f)
		if got != uint32(want) || score < 20 {
			t.Fatalf("version 2: %s identified as track %d (score %d), want %d", filepath.Base(f), got, score, want)
		}
		scores[got] = score
	}
	if got, _ := identify(t, dir, 1, files[3]); got == 3 {
		t.Error("version 1 must not know the added song")
	}

	if err := compact(dir, segment.EncodingVarint, mergeAll); err != nil {
		t.Fatal(err)
	}
	v3, err := versioned.Latest(dir)
	if err != nil || v3.Version != 3 || len(v3.Partitions[0]) != 1 || len(v3.Partitions[1]) != 1 {
		t.Fatalf("after merge: %+v (%v); want version 3 with one segment per partition", v3, err)
	}
	for want, f := range files {
		if got, score := identify(t, dir, 3, f); got != uint32(want) || score != scores[got] {
			t.Errorf("merged: %s -> track %d score %d, before merging %d score %d", filepath.Base(f), got, score, want, scores[uint32(want)])
		}
	}
}
