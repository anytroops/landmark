// Package segment reads and writes immutable index segments (formats v1, v2).
//
// A segment holds every posting for one partition's tracks. Layout, all
// little-endian:
//
//	Header    32 bytes, see header below
//	Keys      n_keys x u32, sorted unique hashes
//	(pad)     zero bytes up to an 8-byte boundary
//	Offsets   (n_keys + 1) x u64, byte offsets into Postings
//	Postings  per key, postings sorted by (track, t), encoded per Flags
//	Footer    u32 CRC-32C over every byte above
//
// Track ids are local to the partition: local = global / NPartitions (D19).
// Keys are found by binary search (D20); Flags reserves bits so a direct
// lookup table can be added without a version bump.
//
// Version 2 (D34) is version 1 with flagAux set: every posting carries two
// more bytes after its (track, t), the triplet span and the anchor band of
// the B2 fingerprint (D31, D33), in both encodings. A version 1 reader
// would misread those bytes, so it rejects the version number; this
// package reads both versions.
package segment

import (
	"encoding/binary"
	"hash/crc32"
)

// Magic identifies a segment file.
const Magic = "LMRK"

// Version is the format of pair-fingerprint segments; VersionAux is the
// format of segments whose postings carry span and band (D34). This package
// reads both.
const (
	Version    = 1
	VersionAux = 2
)

const (
	headerSize = 32
	footerSize = 4
)

// Encoding selects how postings are stored.
type Encoding uint16

const (
	// EncodingFixed stores each posting as two raw u32s (8 bytes). It is
	// the uncompressed baseline for the index-size benchmark (B4).
	EncodingFixed Encoding = 0
	// EncodingVarint stores, per key: track as a varint gap from the
	// previous posting's track (the first is absolute); t as a raw varint
	// when the track changes, else as a varint delta from the previous t.
	// Sorted order makes every gap and delta non-negative.
	EncodingVarint Encoding = 1
)

const (
	flagEncodingMask = 0x3 // bits 0-1: Encoding
	// Bits 2-3 are reserved for the key lookup structure; 0 = sorted keys
	// with binary search, the only one v1 implements (D20).
	flagLookupMask = 0xC
	// Bit 4: postings carry span and band bytes (version 2 only, D34).
	flagAux = 0x10
	// Every other bit must be zero.
	flagKnownMask = flagEncodingMask | flagLookupMask | flagAux
)

// header is the fixed 32-byte prefix:
//
//	0  magic        [4]byte
//	4  version      u16
//	6  flags        u16
//	8  n_keys       u32
//	12 n_tracks     u32  (local tracks in this partition)
//	16 n_postings   u64
//	24 partition    u32
//	28 n_partitions u32
type header struct {
	Version     uint16
	Flags       uint16
	NKeys       uint32
	NTracks     uint32
	NPostings   uint64
	Partition   uint32
	NPartitions uint32
}

func (h header) encoding() Encoding { return Encoding(h.Flags & flagEncodingMask) }

func (h header) aux() bool { return h.Flags&flagAux != 0 }

// postingBytes is the size of one fixed-encoding posting.
func (h header) postingBytes() int {
	if h.aux() {
		return 10
	}
	return 8
}

func (h header) marshal() []byte {
	b := make([]byte, headerSize)
	copy(b, Magic)
	binary.LittleEndian.PutUint16(b[4:], h.Version)
	binary.LittleEndian.PutUint16(b[6:], h.Flags)
	binary.LittleEndian.PutUint32(b[8:], h.NKeys)
	binary.LittleEndian.PutUint32(b[12:], h.NTracks)
	binary.LittleEndian.PutUint64(b[16:], h.NPostings)
	binary.LittleEndian.PutUint32(b[24:], h.Partition)
	binary.LittleEndian.PutUint32(b[28:], h.NPartitions)
	return b
}

func unmarshalHeader(b []byte) header {
	return header{
		Version:     binary.LittleEndian.Uint16(b[4:]),
		Flags:       binary.LittleEndian.Uint16(b[6:]),
		NKeys:       binary.LittleEndian.Uint32(b[8:]),
		NTracks:     binary.LittleEndian.Uint32(b[12:]),
		NPostings:   binary.LittleEndian.Uint64(b[16:]),
		Partition:   binary.LittleEndian.Uint32(b[24:]),
		NPartitions: binary.LittleEndian.Uint32(b[28:]),
	}
}

// offsetsStart returns where the offsets table starts: after the keys, padded
// to 8 bytes so u64 offsets are naturally aligned.
func offsetsStart(nKeys uint64) uint64 {
	end := headerSize + 4*nKeys
	return (end + 7) &^ 7
}

// crcTable returns the CRC-32C (Castagnoli) table, which modern CPUs compute
// in hardware. crc32 builds it once and caches it, so this is cheap.
func crcTable() *crc32.Table { return crc32.MakeTable(crc32.Castagnoli) }
