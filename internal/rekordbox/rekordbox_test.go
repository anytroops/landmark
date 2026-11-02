package rekordbox

import (
	"os"
	"strings"
	"testing"
)

func parseFixture(t *testing.T) *Library {
	t.Helper()
	f, err := os.Open("../../testdata/rekordbox/collection.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	lib, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

func TestParseTracksGridsAndCues(t *testing.T) {
	lib := parseFixture(t)
	if len(lib.Tracks) != 3 {
		t.Fatalf("got %d tracks, want 3", len(lib.Tracks))
	}
	a := lib.Tracks[0]
	if a.ID != "101" || a.Name != "First Light" || a.Artist != "Test Artist" || a.AverageBPM != 124 || a.TotalTime != 372 {
		t.Errorf("track 101 = %+v", a)
	}
	if len(a.Tempos) != 1 || a.Tempos[0] != (Tempo{Start: 0.025, BPM: 124, Meter: "4/4", Beat: 1}) {
		t.Errorf("tempos = %+v", a.Tempos)
	}
	if len(a.Marks) != 3 {
		t.Fatalf("got %d marks, want 3", len(a.Marks))
	}
	if m := a.Marks[1]; m.Name != "drop" || m.Type != Cue || m.Num != 0 || m.Start != 62.928 || m.End != nil {
		t.Errorf("hot cue = %+v", m)
	}
	if m := a.Marks[2]; m.Type != Loop || m.Num != MemoryCue || m.End == nil || *m.End != 39.218 {
		t.Errorf("loop = %+v", m)
	}
	if b := lib.Tracks[1]; b.Name != "Tab\tName" || b.Artist != "B&C" || len(b.Tempos) != 2 || b.Tempos[1].BPM != 128 {
		t.Errorf("track 102 = %+v", b)
	}
}

func TestPath(t *testing.T) {
	lib := parseFixture(t)
	for i, want := range []string{
		"/Users/dj/Music/House/First Light (Extended Mix).mp3",
		"C:/Music/b&c.wav",
	} {
		got, err := lib.Tracks[i].Path()
		if err != nil || got != want {
			t.Errorf("track %d: Path() = %q, %v; want %q", i, got, err, want)
		}
	}
	if _, err := lib.Tracks[2].Path(); err == nil {
		t.Error("an http location gave a path")
	}
	remote := Track{ID: "x", Location: "file://nas/Music/a.mp3"}
	if _, err := remote.Path(); err == nil {
		t.Error("a file URI on another host gave a path")
	}
}

func TestPlaylistsResolveByIDAndLocation(t *testing.T) {
	lib := parseFixture(t)
	pls := lib.Playlists()
	if len(pls) != 2 {
		t.Fatalf("got playlists %v, want 2", pls)
	}
	fri := pls["Sets/Friday"]
	if fri == nil || len(fri.Tracks) != 2 || fri.Tracks[0].ID != "102" || fri.Tracks[1].ID != "101" {
		t.Fatalf("Sets/Friday = %+v", fri)
	}
	if fri.Tracks[1] != &lib.Tracks[0] {
		t.Error("playlist entries do not point into the collection")
	}
	loc := pls["By location"]
	if loc == nil || len(loc.Tracks) != 1 || loc.Tracks[0].ID != "101" {
		t.Errorf("By location = %+v", loc)
	}
}

func TestParseRejects(t *testing.T) {
	for _, tc := range []struct{ name, xml, want string }{
		{"version", `<DJ_PLAYLISTS Version="2.0.0"><COLLECTION/></DJ_PLAYLISTS>`, "version"},
		{"duplicate id", `<DJ_PLAYLISTS Version="1.0.0"><COLLECTION>
			<TRACK TrackID="1" Location="file://localhost/a.mp3"/>
			<TRACK TrackID="1" Location="file://localhost/b.mp3"/></COLLECTION></DJ_PLAYLISTS>`, "duplicate"},
		{"unknown entry", `<DJ_PLAYLISTS Version="1.0.0"><COLLECTION>
			<TRACK TrackID="1" Location="file://localhost/a.mp3"/></COLLECTION>
			<PLAYLISTS><NODE Type="0" Name="ROOT"><NODE Type="1" Name="P" KeyType="0">
			<TRACK Key="2"/></NODE></NODE></PLAYLISTS></DJ_PLAYLISTS>`, `playlist "P" names track "2"`},
		{"not xml", `{"tracks": []}`, "rekordbox xml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tc.xml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
