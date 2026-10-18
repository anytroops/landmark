package main

import (
	"strings"
	"testing"

	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/triplet"
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

func TestEntriesCarrySpanAndBandOnlyForTriplets(t *testing.T) {
	pcm := make([]float32, 3*11025)
	state := uint32(1)
	for i := range pcm { // seeded noise: plenty of peaks
		state = state*1664525 + 1013904223
		pcm[i] = float32(state)/(1<<32) - 0.5
	}
	pairs := entries(fingerprint.New(fingerprint.DefaultParams()), nil, pcm)
	trips := entries(nil, triplet.New(triplet.DefaultParams()), pcm)
	if len(pairs) == 0 || len(trips) == 0 {
		t.Fatalf("%d pair and %d triplet entries, want both > 0", len(pairs), len(trips))
	}
	for _, e := range pairs {
		if e.Span != 0 || e.Band != 0 || e.Track != 0 {
			t.Fatalf("pair entry %+v carries triplet fields", e)
		}
	}
	for _, e := range trips {
		if e.Span == 0 || e.Track != 0 {
			t.Fatalf("triplet entry %+v: want a span and no track yet", e)
		}
	}
}
