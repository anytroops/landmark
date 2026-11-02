// Package rekordbox reads Rekordbox's XML collection format (DJ_PLAYLISTS
// version 1.0.0, as AlphaTheta's "rekordbox xml format" list documents it):
// a DJ's tracks with their file locations, beat grids, and cues, and their
// playlists. It is the set reconstructor's library import (D54): the songs a
// recorded set is matched against are the DJ's own.
//
// Attributes the package does not use (hot cue colours, ratings, and the
// like) are kept as they were read, so exports from newer Rekordbox versions
// parse and Export writes tracks back without losing anything.
package rekordbox

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

// Library is one collection export.
type Library struct {
	Tracks []Track
	Root   *Node // the PLAYLISTS tree's ROOT folder; nil if the export has none
}

// Track is one COLLECTION entry.
type Track struct {
	ID         string     `xml:"TrackID,attr"` // text: only playlists refer to it
	Name       string     `xml:"Name,attr,omitempty"`
	Artist     string     `xml:"Artist,attr,omitempty"`
	Album      string     `xml:"Album,attr,omitempty"`
	Genre      string     `xml:"Genre,attr,omitempty"`
	Kind       string     `xml:"Kind,attr,omitempty"`
	TotalTime  float64    `xml:"TotalTime,attr,omitempty"` // seconds
	AverageBPM float64    `xml:"AverageBpm,attr,omitempty"`
	Tonality   string     `xml:"Tonality,attr,omitempty"`
	Location   string     `xml:"Location,attr"` // file URI, as exported
	Other      []xml.Attr `xml:",any,attr"`     // every other attribute, as read
	Tempos     []Tempo    `xml:"TEMPO"`
	Marks      []Mark     `xml:"POSITION_MARK"`
}

// Tempo is one beat grid section.
type Tempo struct {
	Start float64    `xml:"Inizio,attr"` // seconds
	BPM   float64    `xml:"Bpm,attr"`
	Meter string     `xml:"Metro,attr"` // e.g. 4/4
	Beat  int        `xml:"Battito,attr"`
	Other []xml.Attr `xml:",any,attr"`
}

// MarkType is a POSITION_MARK's Type.
type MarkType int

// Mark types, as numbered in the format.
const (
	Cue MarkType = iota
	FadeIn
	FadeOut
	Load
	Loop
)

// MemoryCue is the Num of a memory cue; hot cues A, B, C, ... are 0, 1, 2, ...
const MemoryCue = -1

// Mark is a cue or loop.
type Mark struct {
	Name  string     `xml:"Name,attr"`
	Type  MarkType   `xml:"Type,attr"`
	Start float64    `xml:"Start,attr"`         // seconds
	End   *float64   `xml:"End,attr,omitempty"` // seconds; loops only
	Num   int        `xml:"Num,attr"`
	Other []xml.Attr `xml:",any,attr"` // e.g. a hot cue's Red, Green, Blue
}

// Node is a playlist folder or a playlist.
type Node struct {
	Name     string
	Folder   bool
	Children []*Node  // folders only
	Tracks   []*Track // playlists only, in order
}

// Path returns the track's local file path from its Location: the URI's path
// with escapes decoded, without the leading slash before a Windows drive
// letter.
func (t *Track) Path() (string, error) {
	u, err := url.Parse(t.Location)
	if err != nil {
		return "", fmt.Errorf("track %s: location %q: %w", t.ID, t.Location, err)
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("track %s: location %q is not a file URI", t.ID, t.Location)
	}
	if u.Host != "" && u.Host != "localhost" {
		return "", fmt.Errorf("track %s: location %q is on host %q", t.ID, t.Location, u.Host)
	}
	if winDrive.MatchString(u.Path) {
		return u.Path[1:], nil
	}
	return u.Path, nil
}

var winDrive = regexp.MustCompile(`^/[A-Za-z]:/`)

// Playlists returns every playlist under the root, depth first, with its
// folder path ("Sets/2026/Friday").
func (l *Library) Playlists() map[string]*Node {
	out := map[string]*Node{}
	var walk func(n *Node, prefix string)
	walk = func(n *Node, prefix string) {
		for _, c := range n.Children {
			p := c.Name
			if prefix != "" {
				p = prefix + "/" + c.Name
			}
			if c.Folder {
				walk(c, p)
			} else {
				out[p] = c
			}
		}
	}
	if l.Root != nil {
		walk(l.Root, "")
	}
	return out
}

// The file's structure; Parse turns it into a Library.
type xmlFile struct {
	XMLName    xml.Name `xml:"DJ_PLAYLISTS"`
	Version    string   `xml:"Version,attr"`
	Collection struct {
		Tracks []Track `xml:"TRACK"`
	} `xml:"COLLECTION"`
	Playlists struct {
		Root *xmlNode `xml:"NODE"`
	} `xml:"PLAYLISTS"`
}

type xmlNode struct {
	Type     int        `xml:"Type,attr"` // 0 folder, 1 playlist
	Name     string     `xml:"Name,attr"`
	KeyType  int        `xml:"KeyType,attr"` // 0 TrackID, 1 Location
	Children []*xmlNode `xml:"NODE"`
	Keys     []struct {
		Key string `xml:"Key,attr"`
	} `xml:"TRACK"`
}

// Parse reads a collection export. A playlist entry naming a track not in the
// collection is an error, as is a duplicate TrackID.
func Parse(r io.Reader) (*Library, error) {
	var f xmlFile
	if err := xml.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("rekordbox xml: %w", err)
	}
	if f.Version != "1.0.0" {
		return nil, fmt.Errorf("rekordbox xml: version %q, want 1.0.0", f.Version)
	}
	lib := &Library{Tracks: f.Collection.Tracks}
	byID := make(map[string]*Track, len(lib.Tracks))
	byLocation := make(map[string]*Track, len(lib.Tracks))
	for i := range lib.Tracks {
		t := &lib.Tracks[i]
		if _, dup := byID[t.ID]; dup {
			return nil, fmt.Errorf("rekordbox xml: duplicate TrackID %q", t.ID)
		}
		byID[t.ID] = t
		byLocation[t.Location] = t
	}
	if f.Playlists.Root == nil {
		return lib, nil
	}
	var convert func(x *xmlNode, path string) (*Node, error)
	convert = func(x *xmlNode, path string) (*Node, error) {
		n := &Node{Name: x.Name, Folder: x.Type == 0}
		for _, c := range x.Children {
			cn, err := convert(c, path+"/"+c.Name)
			if err != nil {
				return nil, err
			}
			n.Children = append(n.Children, cn)
		}
		for _, k := range x.Keys {
			index := byID
			if x.KeyType == 1 {
				index = byLocation
			}
			t, ok := index[k.Key]
			if !ok {
				return nil, fmt.Errorf("rekordbox xml: playlist %q names track %q, not in the collection", strings.TrimPrefix(path, "/"), k.Key)
			}
			n.Tracks = append(n.Tracks, t)
		}
		return n, nil
	}
	root, err := convert(f.Playlists.Root, "")
	if err != nil {
		return nil, err
	}
	lib.Root = root
	return lib, nil
}
