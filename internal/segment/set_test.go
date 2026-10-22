package segment

import (
	"path/filepath"
	"testing"
)

func TestNewSetRejectsMismatchedMembers(t *testing.T) {
	dir := t.TempDir()
	open := func(name string, m Meta) *Segment {
		path := filepath.Join(dir, name)
		if err := Write(path, m, []Entry{{Hash: 1, Track: m.NTracks - 1, T: 2}}); err != nil {
			t.Fatal(err)
		}
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	base := open("base", Meta{Partition: 1, NPartitions: 2, NTracks: 10})
	tests := []struct {
		name  string
		delta Meta
		ok    bool
	}{
		{"continues the ids", Meta{Partition: 1, NPartitions: 2, NTracks: 12}, true},
		{"other partition", Meta{Partition: 0, NPartitions: 2, NTracks: 12}, false},
		{"other partitioning", Meta{Partition: 1, NPartitions: 4, NTracks: 12}, false},
		{"other landmark kind", Meta{Partition: 1, NPartitions: 2, NTracks: 12, Aux: true}, false},
		{"fewer tracks than the base", Meta{Partition: 1, NPartitions: 2, NTracks: 9}, false},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, err := NewSet([]*Segment{base, open(string(rune('a'+i)), tt.delta)})
			if (err == nil) != tt.ok {
				t.Fatalf("NewSet: %v, want ok=%v", err, tt.ok)
			}
			if tt.ok && (set.NTracks() != 12 || set.GlobalTrack(11) != 23) {
				t.Errorf("tracks %d, global(11) = %d", set.NTracks(), set.GlobalTrack(11))
			}
		})
	}
	if _, err := NewSet(nil); err == nil {
		t.Error("empty set: want an error")
	}
}
