package segment

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"sort"
)

// ErrCorrupt marks a segment that fails validation or decoding. A shard
// should refuse to serve it, never crash on it.
var ErrCorrupt = errors.New("segment: corrupt")

// Posting is one decoded posting. Track is the partition-local id.
type Posting struct {
	Track uint32
	T     uint32
}

// Segment is a read-only view over segment bytes, usually memory-mapped.
// It is safe for concurrent use: lookups only read.
type Segment struct {
	hdr      header
	data     []byte // whole file
	keys     []byte // n_keys x u32
	offsets  []byte // (n_keys + 1) x u64
	postings []byte
	closer   func() error
}

// FromBytes validates b and returns a Segment over it without copying.
// Every structural invariant is checked here, so lookups can rely on them.
func FromBytes(b []byte) (*Segment, error) {
	if len(b) < headerSize+footerSize {
		return nil, fmt.Errorf("%w: %d bytes is shorter than header + footer", ErrCorrupt, len(b))
	}
	if string(b[:4]) != Magic {
		return nil, fmt.Errorf("%w: bad magic %q", ErrCorrupt, b[:4])
	}
	body := b[:len(b)-footerSize]
	if got, want := crc32.Checksum(body, crcTable()), binary.LittleEndian.Uint32(b[len(body):]); got != want {
		return nil, fmt.Errorf("%w: checksum %08x, footer says %08x", ErrCorrupt, got, want)
	}
	h := unmarshalHeader(b)
	switch {
	case h.Version != Version:
		return nil, fmt.Errorf("%w: version %d, want %d", ErrCorrupt, h.Version, Version)
	case h.Flags&flagLookupMask != 0:
		return nil, fmt.Errorf("%w: unsupported lookup flags %#x", ErrCorrupt, h.Flags)
	case h.encoding() != EncodingFixed && h.encoding() != EncodingVarint:
		return nil, fmt.Errorf("%w: unknown encoding %d", ErrCorrupt, h.encoding())
	case h.NPartitions == 0 || h.Partition >= h.NPartitions:
		return nil, fmt.Errorf("%w: partition %d of %d", ErrCorrupt, h.Partition, h.NPartitions)
	}

	nKeys := uint64(h.NKeys)
	offStart := offsetsStart(nKeys)
	postStart := offStart + 8*(nKeys+1)
	if postStart > uint64(len(body)) {
		return nil, fmt.Errorf("%w: %d keys do not fit in %d bytes", ErrCorrupt, nKeys, len(body))
	}
	s := &Segment{
		hdr:      h,
		data:     b,
		keys:     body[headerSize : headerSize+4*nKeys],
		offsets:  body[offStart:postStart],
		postings: body[postStart:],
	}
	for i := uint64(1); i < nKeys; i++ {
		if s.key(i) <= s.key(i-1) {
			return nil, fmt.Errorf("%w: keys not strictly increasing at %d", ErrCorrupt, i)
		}
	}
	if s.offset(0) != 0 || s.offset(nKeys) != uint64(len(s.postings)) {
		return nil, fmt.Errorf("%w: offsets do not span the postings blob", ErrCorrupt)
	}
	for i := uint64(1); i <= nKeys; i++ {
		if s.offset(i) < s.offset(i-1) {
			return nil, fmt.Errorf("%w: offsets decrease at %d", ErrCorrupt, i)
		}
	}
	if h.encoding() == EncodingFixed && uint64(len(s.postings)) != 8*h.NPostings {
		return nil, fmt.Errorf("%w: fixed postings blob is %d bytes, want %d", ErrCorrupt, len(s.postings), 8*h.NPostings)
	}
	return s, nil
}

func (s *Segment) key(i uint64) uint32 { return binary.LittleEndian.Uint32(s.keys[4*i:]) }

func (s *Segment) offset(i uint64) uint64 { return binary.LittleEndian.Uint64(s.offsets[8*i:]) }

// Key returns the i-th hash in sorted order, for 0 <= i < NKeys().
func (s *Segment) Key(i int) uint32 { return s.key(uint64(i)) }

// NKeys returns the number of distinct hashes.
func (s *Segment) NKeys() int { return int(s.hdr.NKeys) }

// NPostings returns the number of postings.
func (s *Segment) NPostings() uint64 { return s.hdr.NPostings }

// NTracks returns the number of local tracks.
func (s *Segment) NTracks() uint32 { return s.hdr.NTracks }

// Partition returns (partition, n_partitions).
func (s *Segment) Partition() (uint32, uint32) { return s.hdr.Partition, s.hdr.NPartitions }

// Encoding returns the postings encoding.
func (s *Segment) Encoding() Encoding { return s.hdr.encoding() }

// SizeBytes returns the total file size.
func (s *Segment) SizeBytes() int { return len(s.data) }

// GlobalTrack converts a partition-local track id to the catalog-wide id
// (D19: global = local * n_partitions + partition).
func (s *Segment) GlobalTrack(local uint32) uint32 {
	return local*s.hdr.NPartitions + s.hdr.Partition
}

// AppendPostings appends the postings for hash to dst. A missing hash
// appends nothing. It returns ErrCorrupt rather than panicking on bad data.
func (s *Segment) AppendPostings(dst []Posting, hash uint32) ([]Posting, error) {
	n := uint64(s.hdr.NKeys)
	i := uint64(sort.Search(int(n), func(i int) bool { return s.key(uint64(i)) >= hash }))
	if i == n || s.key(i) != hash {
		return dst, nil
	}
	run := s.postings[s.offset(i):s.offset(i+1)]
	if s.hdr.encoding() == EncodingFixed {
		if len(run)%8 != 0 {
			return dst, fmt.Errorf("%w: key %d: fixed run of %d bytes", ErrCorrupt, hash, len(run))
		}
		for p := 0; p < len(run); p += 8 {
			dst = append(dst, Posting{
				Track: binary.LittleEndian.Uint32(run[p:]),
				T:     binary.LittleEndian.Uint32(run[p+4:]),
			})
		}
		return dst, nil
	}
	return decodeVarint(dst, run, hash, s.hdr.NTracks)
}

func decodeVarint(dst []Posting, run []byte, hash, nTracks uint32) ([]Posting, error) {
	var track, t uint64
	for first := true; len(run) > 0; first = false {
		a, n := binary.Uvarint(run)
		if n <= 0 {
			return dst, fmt.Errorf("%w: key %d: bad track varint", ErrCorrupt, hash)
		}
		run = run[n:]
		b, n := binary.Uvarint(run)
		if n <= 0 {
			return dst, fmt.Errorf("%w: key %d: bad t varint", ErrCorrupt, hash)
		}
		run = run[n:]
		switch {
		case first:
			track, t = a, b
		case a == 0:
			t += b
		default:
			track += a
			t = b
		}
		if track >= uint64(nTracks) || t > 0xFFFFFFFF {
			return dst, fmt.Errorf("%w: key %d: posting (%d, %d) out of range", ErrCorrupt, hash, track, t)
		}
		dst = append(dst, Posting{Track: uint32(track), T: uint32(t)})
	}
	return dst, nil
}

// Close releases the mapping, if any. The Segment must not be used after.
func (s *Segment) Close() error {
	if s.closer == nil {
		return nil
	}
	err := s.closer()
	s.closer = nil
	return err
}
