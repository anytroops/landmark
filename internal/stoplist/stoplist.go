// Package stoplist records how many postings each common hash has across a
// whole index, so the coordinator can drop a query's least informative
// landmarks before fan-out (D30).
//
// Hashes from held notes and low frequencies repeat across many songs: on
// fma_medium the longest 0.1% of posting lists produce about a third of the
// postings a query scans. Skipping hashes whose list is longer than a cutoff
// works like a stop list in text search. The counts are global, summed over
// every partition, so a query keeps the same landmarks however the index is
// split and answers stay identical across partition counts.
//
// File format v1, little-endian:
//
//	0  magic      "LMST"
//	4  version    u16 (1)
//	6  reserved   u16 (0)
//	8  n_tracks   u32  tracks in the index the counts cover
//	12 min_count  u32  every hash with at least this many postings is listed
//	16 n_entries  u32
//	20 reserved   u32 (0)
//	24 entries    n_entries x (hash u32, count u32), strictly increasing hash
//	.. crc32c     u32  CRC-32C (Castagnoli) of every byte before it
package stoplist

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"slices"

	"github.com/anytroops/landmark/internal/segment"
)

const (
	// FileName is the stop list's name inside an index directory.
	FileName = "stoplist.lmst"
	// DefaultMinFraction lists hashes with at least 0.2% of the catalog's
	// track count in postings (47 on fma_medium), so any stop_fraction down
	// to 0.002 can be served.
	DefaultMinFraction = 0.002

	magic      = "LMST"
	version    = 1
	headerSize = 24
	entrySize  = 8
	footerSize = 4
)

// ManifestEntry is the "stoplist" field of index.json.
type ManifestEntry struct {
	File   string `json:"file"`
	CRC32C string `json:"crc32c"` // 8 hex digits
}

// ErrCorrupt reports a file that fails validation.
var ErrCorrupt = errors.New("stoplist: corrupt file")

// List is an immutable set of (hash, global posting count) for every hash
// with at least MinCount postings. It is safe for concurrent use.
type List struct {
	nTracks  uint32
	minCount uint32
	hashes   []uint32
	counts   []uint32
	crc      uint32
}

// NTracks is the number of tracks in the index the counts cover.
func (l *List) NTracks() uint32 { return l.nTracks }

// MinCount is the smallest count listed; shorter lists are not recorded.
func (l *List) MinCount() uint32 { return l.minCount }

// Len is the number of listed hashes.
func (l *List) Len() int { return len(l.hashes) }

// Checksum is the CRC-32C of the encoded file, as written to index.json.
func (l *List) Checksum() uint32 { return l.crc }

// New returns a list of hashes (strictly increasing) with their global
// posting counts, each at least minCount, over an index of nTracks tracks.
func New(nTracks, minCount uint32, hashes, counts []uint32) (*List, error) {
	switch {
	case minCount == 0:
		return nil, fmt.Errorf("%w: min_count 0", ErrCorrupt)
	case len(hashes) != len(counts):
		return nil, fmt.Errorf("%w: %d hashes, %d counts", ErrCorrupt, len(hashes), len(counts))
	}
	for i := range hashes {
		switch {
		case i > 0 && hashes[i] <= hashes[i-1]:
			return nil, fmt.Errorf("%w: hashes not strictly increasing at entry %d", ErrCorrupt, i)
		case counts[i] < minCount:
			return nil, fmt.Errorf("%w: entry %d count %d below min_count %d", ErrCorrupt, i, counts[i], minCount)
		}
	}
	l := &List{nTracks: nTracks, minCount: minCount, hashes: hashes, counts: counts}
	l.crc = crc32.Checksum(l.encodeBody(), crcTable())
	return l, nil
}

// MinCountFor returns the listing threshold for a minimum cutoff given as a
// fraction of nTracks: the largest count that fraction allows, at least 1.
func MinCountFor(nTracks uint32, minFraction float64) uint32 {
	return max(1, uint32(math.Floor(minFraction*float64(nTracks))))
}

// Limit converts a cutoff, as a fraction of catalog tracks, into the largest
// posting count a hash may have and stay in a query. It fails if the cutoff
// is below what the list records, since shorter lists are not in it.
func (l *List) Limit(fraction float64) (uint32, error) {
	if fraction <= 0 || math.IsNaN(fraction) {
		return 0, fmt.Errorf("stoplist: cutoff fraction %v must be positive", fraction)
	}
	limit := math.Floor(fraction * float64(l.nTracks))
	if limit < float64(l.minCount) {
		return 0, fmt.Errorf("stoplist: cutoff %v x %d tracks = %v postings is below the list's minimum %d; rebuild with a smaller minimum",
			fraction, l.nTracks, limit, l.minCount)
	}
	return uint32(min(limit, math.MaxUint32)), nil
}

// Count returns hash's global posting count, or 0 if it has fewer than
// MinCount postings.
func (l *List) Count(hash uint32) uint32 {
	i, ok := slices.BinarySearch(l.hashes, hash)
	if !ok {
		return 0
	}
	return l.counts[i]
}

// Keep reports which landmarks survive: keep[i] is false when hashes[i] has
// more than limit postings. It returns a nil keep when every landmark
// survives, and how many were dropped.
func (l *List) Keep(hashes []uint32, limit uint32) (keep []bool, dropped int) {
	for i, h := range hashes {
		kept := l.Count(h) <= limit
		if !kept && keep == nil { // first drop: everything before it was kept
			keep = make([]bool, len(hashes))
			for j := range i {
				keep[j] = true
			}
		}
		if keep != nil {
			keep[i] = kept
		}
		if !kept {
			dropped++
		}
	}
	return keep, dropped
}

// Compact returns the elements of xs that keep marks (xs itself when keep is
// nil), as a new slice; xs is never modified. Apply it to every array of a
// query (hashes, times, and for triplets spans and bands) to keep them in step.
func Compact[T any](xs []T, keep []bool) []T {
	if keep == nil {
		return xs
	}
	out := make([]T, 0, len(xs))
	for i, x := range xs {
		if keep[i] {
			out = append(out, x)
		}
	}
	return out
}

// Filter returns the landmarks whose hash has at most limit postings, and
// how many were dropped. It returns the inputs unchanged when nothing is
// dropped, and otherwise new slices; the inputs are never modified.
func (l *List) Filter(hashes, times []uint32, limit uint32) ([]uint32, []uint32, int) {
	keep, dropped := l.Keep(hashes, limit)
	return Compact(hashes, keep), Compact(times, keep), dropped
}

// FromSegments counts every hash's postings across all partitions of one
// index and lists those with at least minCount. Each segment's keys are
// sorted, so a P-way merge visits every distinct hash once in order.
func FromSegments(segs []*segment.Segment, minCount uint32) (*List, error) {
	if minCount == 0 {
		return nil, errors.New("stoplist: minCount must be at least 1")
	}
	l := &List{minCount: minCount}
	next := make([]int, len(segs)) // next[s] = next key index to visit in segs[s]
	var buf []segment.Posting
	for {
		cur, found := uint32(0), false
		for s, seg := range segs {
			if next[s] < seg.NKeys() {
				if k := seg.Key(next[s]); !found || k < cur {
					cur, found = k, true
				}
			}
		}
		if !found {
			break
		}
		var count uint64
		for s, seg := range segs {
			if next[s] < seg.NKeys() && seg.Key(next[s]) == cur {
				var err error
				if buf, err = seg.AppendPostings(buf[:0], cur); err != nil {
					return nil, fmt.Errorf("stoplist: hash %d: %w", cur, err)
				}
				count += uint64(len(buf))
				next[s]++
			}
		}
		if count >= uint64(minCount) {
			l.hashes = append(l.hashes, cur)
			l.counts = append(l.counts, uint32(min(count, math.MaxUint32)))
		}
	}
	for _, seg := range segs {
		l.nTracks += seg.NTracks()
	}
	l.crc = crc32.Checksum(l.encodeBody(), crcTable())
	return l, nil
}

// Write saves the list to path.
func (l *List) Write(path string) error {
	b := l.encodeBody()
	b = binary.LittleEndian.AppendUint32(b, crc32.Checksum(b, crcTable()))
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("stoplist: %w", err)
	}
	return nil
}

func (l *List) encodeBody() []byte {
	b := make([]byte, headerSize, headerSize+entrySize*len(l.hashes)+footerSize)
	copy(b, magic)
	binary.LittleEndian.PutUint16(b[4:], version)
	binary.LittleEndian.PutUint32(b[8:], l.nTracks)
	binary.LittleEndian.PutUint32(b[12:], l.minCount)
	binary.LittleEndian.PutUint32(b[16:], uint32(len(l.hashes)))
	for i, h := range l.hashes {
		b = binary.LittleEndian.AppendUint32(b, h)
		b = binary.LittleEndian.AppendUint32(b, l.counts[i])
	}
	return b
}

// Load reads and validates the list at path.
func Load(path string) (*List, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("stoplist: %w", err)
	}
	return Decode(b)
}

// Decode validates b and returns the list it encodes. Every structural
// invariant is checked, so lookups can rely on them.
func Decode(b []byte) (*List, error) {
	if len(b) < headerSize+footerSize {
		return nil, fmt.Errorf("%w: %d bytes", ErrCorrupt, len(b))
	}
	if string(b[:4]) != magic {
		return nil, fmt.Errorf("%w: bad magic %q", ErrCorrupt, b[:4])
	}
	if v := binary.LittleEndian.Uint16(b[4:]); v != version {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrCorrupt, v)
	}
	body := b[:len(b)-footerSize]
	crc := crc32.Checksum(body, crcTable())
	if want := binary.LittleEndian.Uint32(b[len(b)-footerSize:]); crc != want {
		return nil, fmt.Errorf("%w: checksum %08x, footer says %08x", ErrCorrupt, crc, want)
	}
	n := uint64(binary.LittleEndian.Uint32(b[16:]))
	if uint64(len(body)) != headerSize+entrySize*n {
		return nil, fmt.Errorf("%w: %d entries need %d bytes, have %d", ErrCorrupt, n, headerSize+entrySize*n, len(body))
	}
	hashes, counts := make([]uint32, n), make([]uint32, n)
	for i := range hashes {
		e := body[headerSize+entrySize*i:]
		hashes[i] = binary.LittleEndian.Uint32(e)
		counts[i] = binary.LittleEndian.Uint32(e[4:])
	}
	return New(binary.LittleEndian.Uint32(b[8:]), binary.LittleEndian.Uint32(b[12:]), hashes, counts)
}

func crcTable() *crc32.Table { return crc32.MakeTable(crc32.Castagnoli) }
