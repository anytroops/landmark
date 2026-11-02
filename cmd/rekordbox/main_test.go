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

func TestPlaylistMapsTheSetToTheCollectionWithCues(t *testing.T) {
	lib := fixture(t)
	files := map[int]string{0: "/Users/dj/Music/House/First Light (Extended Mix).mp3", 1: "C:/Music/b&c.wav"}
	audio := func(d int) (string, bool) { p, ok := files[d]; return p, ok }
	f := func(x float64) *float64 { return &x }
	set := []entry{
		{Track: 0, StartS: 0, EndS: 95.5, SourceStartS: f(30.2), SourceEndS: f(125.7)},
		{Track: 1, StartS: 90, EndS: 200},
		{Track: 0, StartS: 3600, EndS: 3700, SourceStartS: f(-0.4), SourceEndS: f(99.6)},
	}
	plain, err := playlist(lib, audio, set, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 3 || plain[0] != &lib.Tracks[0] || plain[1] != &lib.Tracks[1] || plain[2] != &lib.Tracks[0] {
		t.Errorf("playlist without cues = %v", plain)
	}
	cued, err := playlist(lib, audio, set, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Tracks[0].Marks) != 3 {
		t.Fatalf("the collection's own track changed: %d marks", len(lib.Tracks[0].Marks))
	}
	got := cued[0].Marks[3:]
	want := []rekordbox.Mark{
		{Name: "Set in 0:00", Start: 30.2, Num: rekordbox.MemoryCue},
		{Name: "Set out 1:35", Start: 125.7, Num: rekordbox.MemoryCue},
		{Name: "Set in 1:00:00", Start: 0, Num: rekordbox.MemoryCue},
		{Name: "Set out 1:01:40", Start: 99.6, Num: rekordbox.MemoryCue},
	}
	if cued[2] != cued[0] || len(got) != len(want) {
		t.Fatalf("cues = %+v", got)
	}
	for i := range want {
		if got[i].Name != want[i].Name || got[i].Start != want[i].Start || got[i].Num != want[i].Num || got[i].Type != rekordbox.Cue {
			t.Errorf("cue %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if len(cued[1].Marks) != 0 {
		t.Errorf("a song without source positions got cues: %+v", cued[1].Marks)
	}
	if _, err := playlist(lib, audio, append(set, entry{Track: 7, Name: "Stranger"}), false); err == nil ||
		!strings.Contains(err.Error(), "Stranger (track 7)") {
		t.Errorf("a song not in the collection: err = %v", err)
	}
}
