package rekordbox

import (
	"bytes"
	"strings"
	"testing"
)

func TestExportRoundTripsTracksAndKeepsUnknownAttributes(t *testing.T) {
	lib := parseFixture(t)
	a, b := &lib.Tracks[0], &lib.Tracks[1]
	var buf bytes.Buffer
	if err := Export(&buf, "Friday, reconstructed", []*Track{b, a, b}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`Rating="153"`, `Colour="0xFF007F"`, `Size="9000000"`, `Red="40"`, `Green="226"`, `Blue="20"`} {
		if !strings.Contains(out, want) {
			t.Errorf("export lost %s", want)
		}
	}
	got, err := Parse(strings.NewReader(out))
	if err != nil {
		t.Fatalf("export does not parse: %v\n%s", err, out)
	}
	if len(got.Tracks) != 2 {
		t.Fatalf("collection has %d tracks, want 2 (a repeat is listed once)", len(got.Tracks))
	}
	for i, want := range []*Track{b, a} {
		g := got.Tracks[i]
		if g.ID != want.ID || g.Location != want.Location || g.Name != want.Name || g.AverageBPM != want.AverageBPM ||
			len(g.Tempos) != len(want.Tempos) || len(g.Marks) != len(want.Marks) {
			t.Errorf("track %d = %+v, want %+v", i, g, *want)
		}
	}
	if m := got.Tracks[1].Marks[2]; m.End == nil || *m.End != 39.218 || m.Type != Loop {
		t.Errorf("loop after round trip = %+v", m)
	}
	pl := got.Playlists()["Friday, reconstructed"]
	if pl == nil || len(pl.Tracks) != 3 || pl.Tracks[0].ID != "102" || pl.Tracks[1].ID != "101" || pl.Tracks[2].ID != "102" {
		t.Errorf("playlist = %+v", pl)
	}
}

func TestExportRefusesATrackWithoutIdentity(t *testing.T) {
	var buf bytes.Buffer
	if err := Export(&buf, "x", []*Track{{Name: "no id"}}); err == nil {
		t.Error("a track without TrackID or Location was exported")
	}
}
