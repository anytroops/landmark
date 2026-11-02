// Command rekordbox works with a Rekordbox collection export (File > Export
// Collection in xml format) for the set reconstructor (D54):
//
//	rekordbox extras -xml collection.xml > library.tsv
//	ingest -manifest "" -extra library.tsv -fingerprint triplets -out data/segments/mine_b2
//
// extras writes the collection's tracks (or one playlist's, -playlist
// "Folder/Name") as the name<TAB>path list cmd/ingest -extra takes. Tracks
// that are not local files, or whose files are missing, are skipped and
// counted on stderr; a file in the collection twice is listed once.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/anytroops/landmark/internal/rekordbox"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 || os.Args[1] != "extras" {
		log.Fatal("usage: rekordbox extras -xml collection.xml [-playlist Folder/Name]")
	}
	fs := flag.NewFlagSet("extras", flag.ExitOnError)
	xmlPath := fs.String("xml", "", "Rekordbox collection export (required)")
	playlist := fs.String("playlist", "", "only this playlist's tracks, by folder path (default: the whole collection)")
	_ = fs.Parse(os.Args[2:])
	if *xmlPath == "" {
		log.Fatal("-xml is required")
	}
	f, err := os.Open(*xmlPath)
	if err != nil {
		log.Fatal(err)
	}
	lib, err := rekordbox.Parse(f)
	_ = f.Close()
	if err != nil {
		log.Fatal(err)
	}
	exists := func(p string) bool {
		st, err := os.Stat(p)
		return err == nil && st.Mode().IsRegular()
	}
	lines, skipped, err := extras(lib, *playlist, exists)
	if err != nil {
		log.Fatal(err)
	}
	if err := write(os.Stdout, lines); err != nil {
		log.Fatal(err)
	}
	log.Printf("%d tracks listed, %d skipped", len(lines), len(skipped))
	for _, s := range skipped {
		log.Printf("skipped: %s", s)
	}
}

// extras returns the name<TAB>path lines for the collection or one of its
// playlists, in collection (or playlist) order, and why each skipped track
// was skipped.
func extras(lib *rekordbox.Library, playlist string, exists func(string) bool) (lines, skipped []string, err error) {
	tracks := make([]*rekordbox.Track, len(lib.Tracks))
	for i := range lib.Tracks {
		tracks[i] = &lib.Tracks[i]
	}
	if playlist != "" {
		pls := lib.Playlists()
		p, ok := pls[playlist]
		if !ok {
			names := make([]string, 0, len(pls))
			for n := range pls {
				names = append(names, n)
			}
			sort.Strings(names)
			return nil, nil, fmt.Errorf("no playlist %q; the export has: %s", playlist, strings.Join(names, ", "))
		}
		tracks = p.Tracks
	}
	seen := map[string]bool{}
	for _, t := range tracks {
		path, err := t.Path()
		switch {
		case err != nil:
			skipped = append(skipped, err.Error())
			continue
		case path != strings.TrimSpace(path) || strings.ContainsAny(path, "\t\n"):
			skipped = append(skipped, fmt.Sprintf("track %s: path %q has spaces at an end or a tab or newline, which -extra cannot take", t.ID, path))
			continue
		case !exists(path):
			skipped = append(skipped, fmt.Sprintf("track %s: no file at %s", t.ID, path))
			continue
		case seen[path]:
			continue
		}
		seen[path] = true
		lines = append(lines, name(t)+"\t"+path)
	}
	if len(lines) == 0 {
		return nil, skipped, errors.New("no track in the export is a local file that exists")
	}
	return lines, skipped, nil
}

// name is "Artist - Title" on one line. It never starts with "#", which
// -extra would read as a comment.
func name(t *rekordbox.Track) string {
	clean := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	artist, title := clean(t.Artist), clean(t.Name)
	if artist == "" {
		artist = "Unknown artist"
	}
	if title == "" {
		title = "Untitled " + t.ID
	}
	n := artist + " - " + title
	if strings.HasPrefix(n, "#") {
		n = "Track " + n
	}
	return n
}

func write(w io.Writer, lines []string) error {
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}
