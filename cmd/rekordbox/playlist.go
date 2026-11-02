package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/anytroops/landmark/internal/catalog"
	"github.com/anytroops/landmark/internal/rekordbox"
)

// entry is the part of a tracklist -json entry the playlist needs.
type entry struct {
	Track        int      `json:"track"`
	Name         string   `json:"name"`
	StartS       float64  `json:"start_s"`
	EndS         float64  `json:"end_s"`
	SourceStartS *float64 `json:"source_start_s"`
	SourceEndS   *float64 `json:"source_end_s"`
}

func playlistCmd(args []string) {
	fs := flag.NewFlagSet("playlist", flag.ExitOnError)
	xmlPath := fs.String("xml", "", "Rekordbox collection export the index was built from (required)")
	index := fs.String("index", "", "the index the set was searched in, for its tracks' files (required)")
	tracklistPath := fs.String("tracklist", "", "tracklist -json output for the set (required)")
	name := fs.String("name", "Reconstructed set", "playlist name")
	cues := fs.Bool("cues", false, "add a memory cue where the set brought each song in and one where it took it out")
	_ = fs.Parse(args)
	if *index == "" || *tracklistPath == "" {
		log.Fatal(usage)
	}
	lib := load(*xmlPath)
	cat, err := catalog.Load(*index)
	if err != nil {
		log.Fatal(err)
	}
	b, err := os.ReadFile(*tracklistPath)
	if err != nil {
		log.Fatal(err)
	}
	var entries []entry
	if err := json.Unmarshal(b, &entries); err != nil {
		log.Fatalf("%s: %v", *tracklistPath, err)
	}
	tracks, err := playlist(lib, func(dense int) (string, bool) { return cat.Audio(dense, "") }, entries, *cues)
	if err != nil {
		log.Fatal(err)
	}
	if err := rekordbox.Export(os.Stdout, *name, tracks); err != nil {
		log.Fatal(err)
	}
}

// playlist maps the set's entries to the collection's tracks through their
// files (audio gives an index track's file), in set order. With cues, each
// track is a copy with a memory cue at every source position the set
// entered and left it.
func playlist(lib *rekordbox.Library, audio func(int) (string, bool), entries []entry, cues bool) ([]*rekordbox.Track, error) {
	byPath := map[string]*rekordbox.Track{}
	for i := range lib.Tracks {
		if p, err := lib.Tracks[i].Path(); err == nil {
			byPath[p] = &lib.Tracks[i]
		}
	}
	copies := map[string]*rekordbox.Track{}
	var out []*rekordbox.Track
	var missing []string
	for _, e := range entries {
		path, ok := audio(e.Track)
		t := byPath[path]
		if !ok || t == nil {
			missing = append(missing, fmt.Sprintf("%s (track %d)", e.Name, e.Track))
			continue
		}
		if cues {
			c, ok := copies[t.ID]
			if !ok {
				cp := *t
				cp.Marks = append([]rekordbox.Mark(nil), t.Marks...)
				c, copies[t.ID] = &cp, &cp
			}
			if e.SourceStartS != nil {
				c.Marks = append(c.Marks, rekordbox.Mark{Name: "Set in " + clock(e.StartS), Type: rekordbox.Cue,
					Start: max(*e.SourceStartS, 0), Num: rekordbox.MemoryCue})
			}
			if e.SourceEndS != nil {
				c.Marks = append(c.Marks, rekordbox.Mark{Name: "Set out " + clock(e.EndS), Type: rekordbox.Cue,
					Start: max(*e.SourceEndS, 0), Num: rekordbox.MemoryCue})
			}
			t = c
		}
		out = append(out, t)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%d songs of the set are not in the collection export: %s", len(missing), strings.Join(missing, "; "))
	}
	return out, nil
}

// clock is h:mm:ss or m:ss.
func clock(s float64) string {
	t := int(s)
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", t/3600, t/60%60, t%60)
	}
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}
