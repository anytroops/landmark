package segment

import (
	"errors"
	"fmt"
)

// Set is one partition's base segment and its delta segments (D42), searched
// as one source: a hash's postings are each segment's postings in turn.
// Matchers sort votes before counting them, so a set answers exactly as one
// segment holding the same postings would.
//
// Deltas continue the partition's local track numbering, and each one's
// track count covers every id up to and including its own tracks, so the
// counts never decrease along the set.
type Set struct {
	segs                   []*Segment
	partition, nPartitions uint32
}

// NewSet checks that the segments belong together: one partition of the
// same partitioning, the same landmark kind, and non-decreasing track
// counts. It takes ownership: Close closes them all.
func NewSet(segs []*Segment) (*Set, error) {
	if len(segs) == 0 {
		return nil, errors.New("segment: empty set")
	}
	base := segs[0]
	part, n := base.Partition()
	for i, s := range segs[1:] {
		p, m := s.Partition()
		switch {
		case p != part || m != n:
			return nil, fmt.Errorf("segment: set member %d is partition %d of %d, base %d of %d", i+1, p, m, part, n)
		case s.HasAux() != base.HasAux():
			return nil, fmt.Errorf("segment: set member %d has another landmark kind than the base", i+1)
		case s.NTracks() < segs[i].NTracks():
			return nil, fmt.Errorf("segment: set member %d covers %d tracks, fewer than the %d before it", i+1, s.NTracks(), segs[i].NTracks())
		}
	}
	return &Set{segs: segs, partition: part, nPartitions: n}, nil
}

// AppendPostings appends every member's postings for hash.
func (s *Set) AppendPostings(dst []Posting, hash uint32) ([]Posting, error) {
	var err error
	for _, seg := range s.segs {
		if dst, err = seg.AppendPostings(dst, hash); err != nil {
			return dst, err
		}
	}
	return dst, nil
}

// AppendAuxPostings appends every member's postings for hash with span and
// band.
func (s *Set) AppendAuxPostings(dst []AuxPosting, hash uint32) ([]AuxPosting, error) {
	var err error
	for _, seg := range s.segs {
		if dst, err = seg.AppendAuxPostings(dst, hash); err != nil {
			return dst, err
		}
	}
	return dst, nil
}

// GlobalTrack converts a partition-local id to the catalog-wide one (D19).
func (s *Set) GlobalTrack(local uint32) uint32 { return local*s.nPartitions + s.partition }

// Partition returns the partition and the partition count.
func (s *Set) Partition() (uint32, uint32) { return s.partition, s.nPartitions }

// NTracks is the partition's local id range: the last member's count.
func (s *Set) NTracks() uint32 { return s.segs[len(s.segs)-1].NTracks() }

// HasAux reports whether postings carry span and band (triplet landmarks).
func (s *Set) HasAux() bool { return s.segs[0].HasAux() }

// Version is the members' format version (all equal, as HasAux is).
func (s *Set) Version() uint16 { return s.segs[0].Version() }

// NKeys sums the members' distinct hashes (a hash in two members counts
// twice).
func (s *Set) NKeys() uint64 {
	var n uint64
	for _, seg := range s.segs {
		n += uint64(seg.NKeys())
	}
	return n
}

// NPostings sums the members' postings.
func (s *Set) NPostings() uint64 {
	var n uint64
	for _, seg := range s.segs {
		n += seg.NPostings()
	}
	return n
}

// Segments returns the members, base first.
func (s *Set) Segments() []*Segment { return s.segs }

// Close closes every member.
func (s *Set) Close() error {
	var errs []error
	for _, seg := range s.segs {
		errs = append(errs, seg.Close())
	}
	return errors.Join(errs...)
}
