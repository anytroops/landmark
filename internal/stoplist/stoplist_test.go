package stoplist

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"path/filepath"
	"slices"
	"testing"

	"github.com/anytroops/landmark/internal/segment"
)

// twoPartitions writes a 2-partition index:
//
//	hash 5:  4 postings in p0
//	hash 10: 3 in p0 + 2 in p1 = 5
//	hash 20: 1 in p1
//	hash 30: 2 in p0 + 2 in p1 = 4 (only reaches the threshold when summed)
func twoPartitions(t *testing.T) []*segment.Segment {
	t.Helper()
	dir := t.TempDir()
	e := func(hash, track, t uint32) segment.Entry { return segment.Entry{Hash: hash, Track: track, T: t} }
	parts := [][]segment.Entry{
		{e(5, 0, 1), e(5, 0, 2), e(5, 1, 3), e(5, 2, 4), e(10, 0, 5), e(10, 1, 6), e(10, 2, 7), e(30, 0, 1), e(30, 1, 1)},
		{e(10, 0, 8), e(10, 1, 9), e(20, 0, 1), e(30, 0, 2), e(30, 1, 2)},
	}
	var segs []*segment.Segment
	for p, entries := range parts {
		path := filepath.Join(dir, "part.lmrk"+string(rune('0'+p)))
		meta := segment.Meta{Partition: uint32(p), NPartitions: 2, NTracks: 3, Encoding: segment.EncodingVarint}
		if err := segment.Write(path, meta, entries); err != nil {
			t.Fatal(err)
		}
		s, err := segment.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		segs = append(segs, s)
	}
	return segs
}

func TestFromSegmentsSumsAcrossPartitions(t *testing.T) {
	l, err := FromSegments(twoPartitions(t), 4)
	if err != nil {
		t.Fatal(err)
	}
	if l.NTracks() != 6 || l.MinCount() != 4 || l.Len() != 3 {
		t.Fatalf("NTracks %d MinCount %d Len %d, want 6 4 3", l.NTracks(), l.MinCount(), l.Len())
	}
	for _, tt := range []struct{ hash, want uint32 }{{5, 4}, {10, 5}, {20, 0}, {30, 4}, {99, 0}} {
		if got := l.Count(tt.hash); got != tt.want {
			t.Errorf("Count(%d) = %d, want %d", tt.hash, got, tt.want)
		}
	}
}

func TestWriteLoadRoundTrip(t *testing.T) {
	l, err := FromSegments(twoPartitions(t), 4)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "stoplist.lmst")
	if err := l.Write(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.NTracks() != l.NTracks() || got.MinCount() != l.MinCount() || got.Checksum() != l.Checksum() ||
		!slices.Equal(got.hashes, l.hashes) || !slices.Equal(got.counts, l.counts) {
		t.Fatalf("round trip changed the list: got %+v, want %+v", got, l)
	}
}

// encode builds a file from raw fields, with a valid checksum unless badCRC.
func encode(magicStr string, ver uint16, nTracks, minCount uint32, entries [][2]uint32, badCRC bool) []byte {
	b := make([]byte, headerSize)
	copy(b, magicStr)
	binary.LittleEndian.PutUint16(b[4:], ver)
	binary.LittleEndian.PutUint32(b[8:], nTracks)
	binary.LittleEndian.PutUint32(b[12:], minCount)
	binary.LittleEndian.PutUint32(b[16:], uint32(len(entries)))
	for _, e := range entries {
		b = binary.LittleEndian.AppendUint32(b, e[0])
		b = binary.LittleEndian.AppendUint32(b, e[1])
	}
	crc := crc32.Checksum(b, crc32.MakeTable(crc32.Castagnoli))
	if badCRC {
		crc++
	}
	return binary.LittleEndian.AppendUint32(b, crc)
}

func TestDecodeRejectsCorruptFiles(t *testing.T) {
	ok := [][2]uint32{{5, 4}, {10, 5}}
	tests := []struct {
		name string
		b    []byte
	}{
		{"empty", nil},
		{"truncated header", encode(magic, version, 6, 4, ok, false)[:10]},
		{"bad magic", encode("NOPE", version, 6, 4, ok, false)},
		{"future version", encode(magic, 2, 6, 4, ok, false)},
		{"bad checksum", encode(magic, version, 6, 4, ok, true)},
		{"zero min_count", encode(magic, version, 6, 0, ok, false)},
		{"unsorted hashes", encode(magic, version, 6, 4, [][2]uint32{{10, 5}, {5, 4}}, false)},
		{"duplicate hash", encode(magic, version, 6, 4, [][2]uint32{{5, 4}, {5, 5}}, false)},
		{"count below min", encode(magic, version, 6, 4, [][2]uint32{{5, 3}}, false)},
	}
	// An entry count that disagrees with the length, with a valid checksum.
	short := encode(magic, version, 6, 4, ok, false)
	short = short[:len(short)-footerSize-entrySize]
	short = binary.LittleEndian.AppendUint32(short, crc32.Checksum(short, crc32.MakeTable(crc32.Castagnoli)))
	tests = append(tests, struct {
		name string
		b    []byte
	}{"entry count mismatch", short})

	if _, err := Decode(encode(magic, version, 6, 4, ok, false)); err != nil {
		t.Fatalf("valid file rejected: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decode(tt.b); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("Decode = %v, want ErrCorrupt", err)
			}
		})
	}
}

func TestLimit(t *testing.T) {
	l := &List{nTracks: 1000, minCount: 20}
	tests := []struct {
		fraction float64
		want     uint32
		wantErr  bool
	}{
		{0.0205, 20, false},
		{0.0425, 42, false},
		{1, 1000, false},
		{0.019, 0, true}, // 19 postings: below what the list records
		{0, 0, true},
		{-0.1, 0, true},
	}
	for _, tt := range tests {
		got, err := l.Limit(tt.fraction)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("Limit(%v) = %d, %v; want %d, err %v", tt.fraction, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestMinCountFor(t *testing.T) {
	tests := []struct {
		nTracks  uint32
		fraction float64
		want     uint32
	}{
		{23985, 0.002, 47},
		{100, 0.002, 1}, // never 0
		{0, 0.002, 1},
	}
	for _, tt := range tests {
		if got := MinCountFor(tt.nTracks, tt.fraction); got != tt.want {
			t.Errorf("MinCountFor(%d, %v) = %d, want %d", tt.nTracks, tt.fraction, got, tt.want)
		}
	}
}

func TestFilter(t *testing.T) {
	l := &List{nTracks: 6, minCount: 4, hashes: []uint32{5, 10, 30}, counts: []uint32{4, 5, 4}}
	hashes := []uint32{1, 5, 10, 10, 30, 99}
	times := []uint32{0, 1, 2, 3, 4, 5}
	orig := slices.Clone(hashes)

	tests := []struct {
		name               string
		limit              uint32
		wantH, wantT       []uint32
		wantDrop           int
		wantSameBackingArr bool
	}{
		{"drop count 5", 4, []uint32{1, 5, 30, 99}, []uint32{0, 1, 4, 5}, 2, false},
		{"keep all", 5, hashes, times, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, tm, drop := l.Filter(hashes, times, tt.limit)
			if !slices.Equal(h, tt.wantH) || !slices.Equal(tm, tt.wantT) || drop != tt.wantDrop {
				t.Fatalf("Filter = %v %v %d, want %v %v %d", h, tm, drop, tt.wantH, tt.wantT, tt.wantDrop)
			}
			if same := len(h) > 0 && &h[0] == &hashes[0]; same != tt.wantSameBackingArr {
				t.Errorf("returned the input slice: %v, want %v", same, tt.wantSameBackingArr)
			}
			if !slices.Equal(hashes, orig) {
				t.Errorf("Filter modified its input: %v", hashes)
			}
		})
	}
}

func TestKeep(t *testing.T) {
	l := &List{nTracks: 6, minCount: 4, hashes: []uint32{5, 10, 30}, counts: []uint32{4, 5, 4}}
	tests := []struct {
		name     string
		hashes   []uint32
		wantKeep []bool
		wantDrop int
	}{
		{"nothing dropped gives nil", []uint32{1, 5, 30}, nil, 0},
		{"first landmark dropped", []uint32{10, 1, 10}, []bool{false, true, false}, 2},
		{"later landmark dropped", []uint32{1, 5, 10, 99}, []bool{true, true, false, true}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keep, drop := l.Keep(tt.hashes, 4)
			if !slices.Equal(keep, tt.wantKeep) || drop != tt.wantDrop || (tt.wantKeep == nil) != (keep == nil) {
				t.Fatalf("Keep = %v, %d; want %v, %d", keep, drop, tt.wantKeep, tt.wantDrop)
			}
			if got := Compact([]byte("abcd")[:len(tt.hashes)], keep); len(got) != len(tt.hashes)-drop {
				t.Errorf("Compact kept %d of %d, want %d", len(got), len(tt.hashes), len(tt.hashes)-drop)
			}
		})
	}
}
