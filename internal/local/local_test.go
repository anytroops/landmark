package local

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/segment"
)

const (
	nParts   = 2
	nTracks  = 6
	perTrack = 40
)

// writeIndex writes a pair index of nTracks over nParts partitions, as
// cmd/ingest lays one out: track d's hashes are 1000*(d+1) + i at time i.
func writeIndex(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	type seg struct {
		Partition uint32 `json:"partition"`
		File      string `json:"file"`
		CRC32C    string `json:"crc32c"`
	}
	var segs []seg
	for p := range uint32(nParts) {
		var entries []segment.Entry
		for d := p; d < nTracks; d += nParts {
			for i := range uint32(perTrack) {
				entries = append(entries, segment.Entry{Hash: 1000*(d+1) + i, Track: d / nParts, T: i})
			}
		}
		file := fmt.Sprintf("part-%03d.lmrk", p)
		meta := segment.Meta{Partition: p, NPartitions: nParts, NTracks: nTracks / nParts, Encoding: segment.EncodingVarint}
		if err := segment.Write(filepath.Join(dir, file), meta, entries); err != nil {
			t.Fatal(err)
		}
		s, err := segment.Open(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		segs = append(segs, seg{Partition: p, File: file, CRC32C: fmt.Sprintf("%08x", s.Checksum())})
		_ = s.Close()
	}
	catalog := make([]map[string]any, nTracks)
	for d := range nTracks {
		catalog[d] = map[string]any{"dense": d, "fma_id": d + 1, "partition": d % nParts, "cluster": d}
	}
	write := func(name string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.json", map[string]any{"n_partitions": nParts, "n_tracks": nTracks, "fingerprint": "pairs", "segments": segs})
	write("catalog.json", catalog)
	return dir
}

func query(d uint32) *landmarkv1.IdentifyRequest {
	req := &landmarkv1.IdentifyRequest{TopK: 3}
	for i := range uint32(perTrack) {
		req.Hashes = append(req.Hashes, 1000*(d+1)+i)
		req.Times = append(req.Times, i+7) // the query starts 7 frames into the track
	}
	return req
}

func TestIdentifyFindsEveryTrackAcrossPartitions(t *testing.T) {
	ix, err := Open(context.Background(), writeIndex(t), Options{Deadline: 5 * time.Second, Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	for d := range uint32(nTracks) {
		resp, err := ix.Identify(context.Background(), query(d))
		if err != nil {
			t.Fatalf("track %d: %v", d, err)
		}
		c := resp.GetCandidates()
		if len(c) == 0 || c[0].GetTrackId() != d || c[0].GetScore() != perTrack || c[0].GetOffset() != -7 {
			t.Errorf("track %d: candidates %v", d, c)
		}
		if resp.GetPartial() || resp.GetPartitionsResponded() != nParts {
			t.Errorf("track %d: %d of %d partitions answered", d, resp.GetPartitionsResponded(), resp.GetPartitionsTotal())
		}
	}
}

func TestOpenRefusesBadInput(t *testing.T) {
	if _, err := Open(context.Background(), t.TempDir(), Options{Deadline: time.Second}); err == nil {
		t.Error("an empty directory opened")
	}
	if _, err := Open(context.Background(), writeIndex(t), Options{}); err == nil {
		t.Error("no deadline, but no error")
	}
	// The index has no stop list, so asking for one must fail, and Open must
	// clean up the shards it started (the race detector and leak-free exit
	// of the test binary cover that).
	if _, err := Open(context.Background(), writeIndex(t), Options{Deadline: time.Second, StopFraction: 0.04}); err == nil {
		t.Error("a stop fraction without a stop list, but no error")
	}
}
