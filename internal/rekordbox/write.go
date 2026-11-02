package rekordbox

import (
	"encoding/xml"
	"fmt"
	"io"
)

// Export writes an import file (Rekordbox: the "rekordbox xml" view under
// Preferences > Advanced > Database): the given tracks as its collection,
// each once and with everything it was read with (attributes this package
// does not know included), and one playlist named name listing them in
// order, repeats kept. Rekordbox changes nothing in the user's collection
// until the user imports from that view.
func Export(w io.Writer, name string, tracks []*Track) error {
	var f outFile
	f.Version = "1.0.0"
	f.Product = outProduct{Name: "Landmark set reconstructor", Version: "1", Company: "Landmark"}
	seen := map[string]bool{}
	for _, t := range tracks {
		if t.ID == "" || t.Location == "" {
			return fmt.Errorf("rekordbox export: track %q has no TrackID or Location", t.Name)
		}
		if !seen[t.ID] {
			seen[t.ID] = true
			f.Collection.Tracks = append(f.Collection.Tracks, *t)
		}
	}
	f.Collection.Entries = len(f.Collection.Tracks)
	byID := 0
	pl := outNode{Type: 1, Name: name, KeyType: &byID, Entries: len(tracks)}
	for _, t := range tracks {
		pl.Keys = append(pl.Keys, outKey{Key: t.ID})
	}
	f.Playlists.Root = outNode{Type: 0, Name: "ROOT", Count: 1, Children: []outNode{pl}}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(f); err != nil {
		return fmt.Errorf("rekordbox export: %w", err)
	}
	_, err := io.WriteString(w, "\n")
	return err
}

type outFile struct {
	XMLName    xml.Name   `xml:"DJ_PLAYLISTS"`
	Version    string     `xml:"Version,attr"`
	Product    outProduct `xml:"PRODUCT"`
	Collection struct {
		Entries int     `xml:"Entries,attr"`
		Tracks  []Track `xml:"TRACK"`
	} `xml:"COLLECTION"`
	Playlists struct {
		Root outNode `xml:"NODE"`
	} `xml:"PLAYLISTS"`
}

type outProduct struct {
	Name    string `xml:"Name,attr"`
	Version string `xml:"Version,attr"`
	Company string `xml:"Company,attr"`
}

type outNode struct {
	Type     int       `xml:"Type,attr"`
	Name     string    `xml:"Name,attr"`
	Count    int       `xml:"Count,attr,omitempty"`   // folders
	KeyType  *int      `xml:"KeyType,attr,omitempty"` // playlists: 0, keys are TrackIDs
	Entries  int       `xml:"Entries,attr,omitempty"` // playlists
	Children []outNode `xml:"NODE"`
	Keys     []outKey  `xml:"TRACK"`
}

type outKey struct {
	Key string `xml:"Key,attr"`
}
