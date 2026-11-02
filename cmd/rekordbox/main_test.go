package main

import (
	"os"
	"strings"
	"testing"

	"github.com/anytroops/landmark/internal/rekordbox"
)

func fixture(t *testing.T) *rekordbox.Library {
	t.Helper()
	f, err := os.Open("../../testdata/rekordbox/collection.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	lib, err := rekordbox.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

func TestExtrasListsLocalFilesThatExist(t *testing.T) {
	lib := fixture(t)
	exists := func(p string) bool { return p == "/Users/dj/Music/House/First Light (Extended Mix).mp3" }
	lines, skipped, err := extras(lib, "", exists)
	if err != nil {
		t.Fatal(err)
	}
	want := "Test Artist - First Light\t/Users/dj/Music/House/First Light (Extended Mix).mp3"
	if len(lines) != 1 || lines[0] != want {
		t.Errorf("lines = %q, want [%q]", lines, want)
	}
	// 102's file is missing here; 103 is not a file URI.
	if len(skipped) != 2 || !strings.Contains(skipped[0], "no file") || !strings.Contains(skipped[1], "not a file URI") {
		t.Errorf("skipped = %q", skipped)
	}
}

func TestExtrasOfAPlaylistKeepItsOrderAndListAFileOnce(t *testing.T) {
	lib := fixture(t)
	lib.Tracks = append(lib.Tracks[:2:2], rekordbox.Track{ID: "104", Name: "Again", Location: lib.Tracks[0].Location})
	all := func(string) bool { return true }
	lines, _, err := extras(lib, "Sets/Friday", all)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "B&C - Tab Name\tC:/Music/b&c.wav") || !strings.HasPrefix(lines[1], "Test Artist") {
		t.Errorf("lines = %q", lines)
	}
	if lines, _, _ = extras(lib, "", all); len(lines) != 2 {
		t.Errorf("a file in the collection twice: lines = %q", lines)
	}
	if _, _, err := extras(lib, "Sets/Saturday", all); err == nil || !strings.Contains(err.Error(), "By location, Sets/Friday") {
		t.Errorf("unknown playlist: err = %v", err)
	}
	if _, _, err := extras(lib, "", func(string) bool { return false }); err == nil {
		t.Error("no file exists, but no error")
	}
}

func TestNameIsOneLineAndNeverAComment(t *testing.T) {
	for _, tc := range []struct {
		artist, title, want string
	}{
		{"A", "B", "A - B"},
		{"", "B", "Unknown artist - B"},
		{"A", "", "A - Untitled 7"},
		{" A\n", "B\tC  D", "A - B C D"},
		{"#1 Fan", "Song", "Track #1 Fan - Song"},
	} {
		got := name(&rekordbox.Track{ID: "7", Artist: tc.artist, Name: tc.title})
		if got != tc.want {
			t.Errorf("name(%q, %q) = %q, want %q", tc.artist, tc.title, got, tc.want)
		}
	}
}
