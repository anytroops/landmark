package segment

import (
	"cmp"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// randomEntries draws n entries over nKeys hashes and nTracks local tracks,
// with no duplicate (hash, track, t) triples (the fingerprinter never makes them).
func randomEntries(seed uint64, n, nKeys, nTracks int, maxT uint32) []Entry {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b9))
	seen := map[Entry]bool{}
	var out []Entry
	for len(out) < n {
		e := Entry{
			Hash:  uint32(rng.IntN(nKeys)) * 2654435761, // spread over the u32 range
			Track: uint32(rng.IntN(nTracks)),
			T:     rng.Uint32N(maxT),
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

// reference groups entries by hash, each run sorted by (track, t).
func reference(entries []Entry) map[uint32][]Posting {
	ref := map[uint32][]Posting{}
	for _, e := range entries {
		ref[e.Hash] = append(ref[e.Hash], Posting{Track: e.Track, T: e.T})
	}
	for _, ps := range ref {
		slices.SortFunc(ps, func(a, b Posting) int {
			return cmp.Or(cmp.Compare(a.Track, b.Track), cmp.Compare(a.T, b.T))
		})
	}
	return ref
}

func writeAndOpen(t *testing.T, meta Meta, entries []Entry) *Segment {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seg.lmrk")
	if err := Write(path, meta, entries); err != nil {
		t.Fatalf("Write: %v", err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name              string
		n, nKeys, nTracks int
		maxT              uint32
	}{
		{"empty", 0, 1, 1, 1},
		{"single posting", 1, 1, 1, 10},
		{"dense keys, few tracks", 5000, 50, 3, 2000},
		{"sparse keys, many tracks", 5000, 4000, 900, 2000},
		{"large t values", 2000, 100, 50, 1 << 31},
	}
	for _, tc := range tests {
		for _, enc := range []Encoding{EncodingFixed, EncodingVarint} {
			t.Run(tc.name+"/"+encName(enc), func(t *testing.T) {
				entries := randomEntries(uint64(tc.n), tc.n, tc.nKeys, tc.nTracks, tc.maxT)
				ref := reference(entries)
				meta := Meta{Partition: 2, NPartitions: 4, NTracks: uint32(tc.nTracks), Encoding: enc}
				s := writeAndOpen(t, meta, slices.Clone(entries))

				if s.NKeys() != len(ref) || s.NPostings() != uint64(tc.n) || s.Encoding() != enc {
					t.Fatalf("header: keys=%d postings=%d enc=%d, want %d %d %d",
						s.NKeys(), s.NPostings(), s.Encoding(), len(ref), tc.n, enc)
				}
				for hash, want := range ref {
					got, err := s.AppendPostings(nil, hash)
					if err != nil {
						t.Fatalf("hash %d: %v", hash, err)
					}
					if !slices.Equal(got, want) {
						t.Fatalf("hash %d: got %v, want %v", hash, got, want)
					}
				}
				// Hashes that are absent return nothing.
				for _, miss := range []uint32{1, 3, 0xFFFFFFFF} {
					if _, ok := ref[miss]; ok {
						continue
					}
					if got, err := s.AppendPostings(nil, miss); err != nil || len(got) != 0 {
						t.Fatalf("missing hash %d: got %v, %v", miss, got, err)
					}
				}
			})
		}
	}
}

func TestVarintIsSmallerThanFixed(t *testing.T) {
	entries := randomEntries(7, 20000, 200, 100, 1300)
	fixed := writeAndOpen(t, Meta{NPartitions: 1, NTracks: 100, Encoding: EncodingFixed}, slices.Clone(entries))
	varint := writeAndOpen(t, Meta{NPartitions: 1, NTracks: 100, Encoding: EncodingVarint}, slices.Clone(entries))
	if varint.SizeBytes() >= fixed.SizeBytes() {
		t.Errorf("varint %d bytes >= fixed %d bytes", varint.SizeBytes(), fixed.SizeBytes())
	}
}

func TestGlobalTrack(t *testing.T) {
	s := writeAndOpen(t, Meta{Partition: 3, NPartitions: 8, NTracks: 10}, nil)
	for local, want := range map[uint32]uint32{0: 3, 1: 11, 9: 75} {
		if got := s.GlobalTrack(local); got != want {
			t.Errorf("GlobalTrack(%d) = %d, want %d", local, got, want)
		}
	}
}

func TestWriteRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		meta    Meta
		entries []Entry
	}{
		{"zero partitions", Meta{NPartitions: 0, NTracks: 1}, nil},
		{"partition out of range", Meta{Partition: 4, NPartitions: 4, NTracks: 1}, nil},
		{"unknown encoding", Meta{NPartitions: 1, NTracks: 1, Encoding: 3}, nil},
		{"track beyond n_tracks", Meta{NPartitions: 1, NTracks: 5}, []Entry{{Hash: 1, Track: 5}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			if err := Write(path, tc.meta, tc.entries); err == nil {
				t.Fatal("Write succeeded, want error")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("failed Write left a file at %s", path)
			}
			if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
				t.Errorf("failed Write left a temp file")
			}
		})
	}
}

// validSegmentBytes returns a small, valid varint segment.
func validSegmentBytes(t testing.TB) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seg.lmrk")
	if err := Write(path, Meta{NPartitions: 2, Partition: 1, NTracks: 20, Encoding: EncodingVarint},
		randomEntries(1, 300, 40, 20, 500)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// reseal recomputes the footer checksum, so tests can reach the
// structural checks that sit behind it.
func reseal(b []byte) []byte {
	body := b[:len(b)-footerSize]
	binary.LittleEndian.PutUint32(b[len(body):], crc32.Checksum(body, crcTable()))
	return b
}

func TestFromBytesRejectsCorruption(t *testing.T) {
	valid := validSegmentBytes(t)
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"empty", func([]byte) []byte { return nil }},
		{"truncated", func(b []byte) []byte { return b[:len(b)/2] }},
		{"bad magic", func(b []byte) []byte { b[0] = 'X'; return reseal(b) }},
		{"flipped body byte", func(b []byte) []byte { b[len(b)/2] ^= 0xFF; return b }},
		{"bad checksum", func(b []byte) []byte { b[len(b)-1] ^= 1; return b }},
		{"future version", func(b []byte) []byte { b[4] = 9; return reseal(b) }},
		{"reserved lookup flag", func(b []byte) []byte { b[6] |= 0x4; return reseal(b) }},
		{"n_keys too large", func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[8:], 1<<30)
			return reseal(b)
		}},
		{"partition out of range", func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[24:], 7)
			return reseal(b)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FromBytes(tc.mutate(slices.Clone(valid)))
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("err = %v, want ErrCorrupt", err)
			}
		})
	}
}

// FuzzFromBytes feeds mutated segments to the reader. Whatever the input,
// FromBytes and AppendPostings must return errors, never panic.
func FuzzFromBytes(f *testing.F) {
	f.Add(validSegmentBytes(f))
	path := filepath.Join(f.TempDir(), "aux.lmrk")
	if err := Write(path, Meta{NPartitions: 1, NTracks: 40, Encoding: EncodingVarint, Aux: true},
		auxEntries(5, 300, 30, 40, 500)); err != nil {
		f.Fatal(err)
	}
	aux, err := os.ReadFile(path)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(aux)
	f.Fuzz(func(_ *testing.T, b []byte) {
		check := func(b []byte) {
			s, err := FromBytes(b)
			if err != nil {
				return
			}
			for i := 0; i < s.NKeys(); i++ {
				_, _ = s.AppendPostings(nil, s.key(uint64(i)))
				if s.HasAux() {
					_, _ = s.AppendAuxPostings(nil, s.key(uint64(i)))
				}
			}
		}
		check(b)
		if len(b) >= headerSize+footerSize {
			// Also try with a valid checksum, or nearly every mutation would
			// stop at the CRC and the decoder would go untested.
			check(reseal(slices.Clone(b)))
		}
	})
}

func encName(e Encoding) string {
	if e == EncodingFixed {
		return "fixed"
	}
	return "varint"
}

// auxEntries is randomEntries with span and band derived from each entry.
func auxEntries(seed uint64, n, nKeys, nTracks int, maxT uint32) []Entry {
	out := randomEntries(seed, n, nKeys, nTracks, maxT)
	for i := range out {
		out[i].Span = uint8(1 + (out[i].T+out[i].Track)%63)
		out[i].Band = uint8((out[i].Hash ^ out[i].T) % 234)
	}
	return out
}

func TestAuxRoundTrip(t *testing.T) {
	for _, enc := range []Encoding{EncodingFixed, EncodingVarint} {
		t.Run(encName(enc), func(t *testing.T) {
			entries := auxEntries(7, 4000, 300, 40, 5000)
			want := map[uint32][]AuxPosting{}
			for _, e := range entries {
				want[e.Hash] = append(want[e.Hash], AuxPosting{Track: e.Track, T: e.T, Span: e.Span, Band: e.Band})
			}
			s := writeAndOpen(t, Meta{Partition: 0, NPartitions: 1, NTracks: 40, Encoding: enc, Aux: true}, entries)
			if s.Version() != VersionAux || !s.HasAux() {
				t.Fatalf("version %d, HasAux %v; want %d, true", s.Version(), s.HasAux(), VersionAux)
			}
			for h, ps := range want {
				slices.SortFunc(ps, func(a, b AuxPosting) int {
					return cmp.Or(cmp.Compare(a.Track, b.Track), cmp.Compare(a.T, b.T),
						cmp.Compare(a.Span, b.Span), cmp.Compare(a.Band, b.Band))
				})
				got, err := s.AppendAuxPostings(nil, h)
				if err != nil || !slices.Equal(got, ps) {
					t.Fatalf("hash %d: AppendAuxPostings = %v, %v; want %v", h, got, err, ps)
				}
				// The generic reader sees the same (track, t) postings.
				plain, err := s.AppendPostings(nil, h)
				if err != nil || len(plain) != len(ps) {
					t.Fatalf("hash %d: AppendPostings = %d postings, %v; want %d", h, len(plain), err, len(ps))
				}
				for i := range ps {
					if plain[i].Track != ps[i].Track || plain[i].T != ps[i].T {
						t.Fatalf("hash %d posting %d: %v, want %v", h, i, plain[i], ps[i])
					}
				}
			}
		})
	}
}

func TestAuxPostingsNeedVersion2(t *testing.T) {
	s := writeAndOpen(t, Meta{NPartitions: 1, NTracks: 10, Encoding: EncodingVarint}, randomEntries(3, 50, 5, 10, 100))
	if s.Version() != Version || s.HasAux() {
		t.Fatalf("pair segment: version %d, HasAux %v", s.Version(), s.HasAux())
	}
	if _, err := s.AppendAuxPostings(nil, s.Key(0)); err == nil {
		t.Fatal("AppendAuxPostings on a version 1 segment succeeded")
	}
}

func TestFromBytesRejectsVersionFlagMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aux.lmrk")
	if err := Write(path, Meta{NPartitions: 1, NTracks: 40, Encoding: EncodingVarint, Aux: true},
		auxEntries(5, 300, 30, 40, 500)); err != nil {
		t.Fatal(err)
	}
	aux, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pair := validSegmentBytes(t)
	tests := []struct {
		name string
		b    []byte
	}{
		{"version 2 without the aux flag", func() []byte { b := slices.Clone(aux); b[6] &^= flagAux; return reseal(b) }()},
		{"version 1 with the aux flag", func() []byte { b := slices.Clone(pair); b[6] |= flagAux; return reseal(b) }()},
		{"unknown flag bit", func() []byte { b := slices.Clone(pair); b[7] |= 0x80; return reseal(b) }()},
		{"aux bytes cut off", func() []byte {
			b := slices.Clone(aux)
			// Drop the last posting's band byte from the blob: shrink the
			// final offset by one and move the footer up.
			body := b[:len(b)-footerSize]
			body = body[:len(body)-1]
			nKeys := binary.LittleEndian.Uint32(b[8:])
			off := offsetsStart(uint64(nKeys)) + 8*uint64(nKeys)
			binary.LittleEndian.PutUint64(body[off:], binary.LittleEndian.Uint64(body[off:])-1)
			return reseal(append(body, 0, 0, 0, 0))
		}()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := FromBytes(tc.b)
			if err == nil { // structurally valid: decoding the damaged run must fail instead
				_, err = s.AppendAuxPostings(nil, s.Key(s.NKeys()-1))
			}
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("err = %v, want ErrCorrupt", err)
			}
		})
	}
}
