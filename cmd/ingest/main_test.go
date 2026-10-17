package main

import (
	"strings"
	"testing"
)

func TestParseExtras(t *testing.T) {
	got, err := parseExtras("# refs\nsong a\t/x/a.mp3\n\n  song b\t/x/b.mp3  \n")
	if err != nil {
		t.Fatal(err)
	}
	want := []Extra{{FMAID: -1, Name: "song a", Path: "/x/a.mp3"}, {FMAID: -2, Name: "song b", Path: "/x/b.mp3"}}
	if len(got) != len(want) {
		t.Fatalf("got %d extras, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("extra %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseExtrasRejectsBadLines(t *testing.T) {
	for _, text := range []string{"no tab here", "\t/x/a.mp3", "name\t  "} {
		if _, err := parseExtras(text); err == nil || !strings.Contains(err.Error(), "line 1") {
			t.Errorf("parseExtras(%q) = %v, want a line-1 error", text, err)
		}
	}
}

func TestBuildCatalogWithExtras(t *testing.T) {
	// Two FMA tracks that are duplicates of each other, then two extras.
	ids := []int{10, 20, -1, -2}
	cat := buildCatalog(ids, map[string][]int{"10": {10, 20}}, 2)
	wantCluster := []uint32{0, 0, 2, 3}
	for i, c := range cat {
		if c.Dense != uint32(i) || c.FMAID != ids[i] || c.Partition != uint32(i)%2 || c.Cluster != wantCluster[i] {
			t.Errorf("catalog[%d] = %+v, want fma_id %d cluster %d", i, c, ids[i], wantCluster[i])
		}
	}
}
