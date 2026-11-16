package rekordbox

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Rekordbox's per-track analysis files from a USB export
// (PIONEER/USBANLZ/.../ANLZ0000.DAT and .EXT): the beat grid and the phrase
// structure, for the lighting work (docs/design/light-show.md). A file is a
// "PMAI" header and tagged sections, big-endian. DAT holds PPTH (the audio
// file's path on the USB) and PQTZ (the beat grid); EXT holds PSSI (the
// phrases), XOR-masked by Rekordbox 6 and later. The layout follows Deep
// Symmetry's public description of the format; prototype/anlz.py is the
// reference this port matches.

// Beat is one beat of the grid.
type Beat struct {
	Number int     // 1 to 4: its place in the bar (1 is the downbeat)
	BPM    float64 // the tempo from this beat on
	Time   float64 // seconds into the track
}

// Phrase is one phrase of the structure.
type Phrase struct {
	Beat int    // the 1-based beat of the grid it starts on
	Kind string // intro, up, down, chorus, verse, bridge, outro
}

// Structure is a track's phrase analysis.
type Structure struct {
	Mood    string // high, mid, low
	EndBeat int
	Phrases []Phrase
}

var (
	pssiMask = []byte{0xCB, 0xE1, 0xEE, 0xFA, 0xE5, 0xEE, 0xAD, 0xEE, 0xE9, 0xD2, 0xE9, 0xEB, 0xE1, 0xE9, 0xF3, 0xE8, 0xE9, 0xF4, 0xE1}
	moods    = map[int]string{1: "high", 2: "mid", 3: "low"}
	verses   = map[int]string{1: "intro", 2: "verse", 3: "verse", 4: "verse", 5: "verse", 6: "verse", 7: "verse", 8: "bridge", 9: "chorus", 10: "outro"}
	kinds    = map[int]map[int]string{1: {1: "intro", 2: "up", 3: "down", 5: "chorus", 6: "outro"}, 2: verses, 3: verses}
)

// errNoTag reports an ANLZ file without the wanted section.
var errNoTag = errors.New("rekordbox anlz: no such tag")

const pssiMaskFrom = 18 // PSSI bytes from the mood field on are masked

type section struct{ off, header, length int }

// sections maps each fourcc to its first section in an ANLZ file.
func sections(data []byte) (map[string]section, error) {
	if len(data) < 12 || string(data[:4]) != "PMAI" {
		return nil, errors.New("rekordbox anlz: not an ANLZ file")
	}
	out := map[string]section{}
	for off := int(binary.BigEndian.Uint32(data[4:8])); off+12 <= len(data); {
		header := int(binary.BigEndian.Uint32(data[off+4 : off+8]))
		length := int(binary.BigEndian.Uint32(data[off+8 : off+12]))
		if length < 12 || off+length > len(data) {
			break
		}
		fourcc := string(data[off : off+4])
		if _, seen := out[fourcc]; !seen {
			out[fourcc] = section{off, header, length}
		}
		off += length
	}
	return out, nil
}

// AudioPath is a DAT file's PPTH: the audio file's path on the USB.
func AudioPath(dat []byte) (string, error) {
	t, err := sections(dat)
	if err != nil {
		return "", err
	}
	s, ok := t["PPTH"]
	if !ok {
		return "", fmt.Errorf("%w PPTH", errNoTag)
	}
	n := int(binary.BigEndian.Uint32(dat[s.off+12 : s.off+16]))
	raw := dat[s.off+16 : s.off+16+n]
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = binary.BigEndian.Uint16(raw[2*i:])
	}
	return strings.TrimRight(string(utf16.Decode(u)), "\x00"), nil
}

// Beats is a DAT file's PQTZ beat grid.
func Beats(dat []byte) ([]Beat, error) {
	t, err := sections(dat)
	if err != nil {
		return nil, err
	}
	s, ok := t["PQTZ"]
	if !ok {
		return nil, fmt.Errorf("%w PQTZ", errNoTag)
	}
	n := int(binary.BigEndian.Uint32(dat[s.off+20 : s.off+24]))
	body := dat[s.off+s.header:]
	if len(body) < 8*n {
		return nil, errors.New("rekordbox anlz: PQTZ shorter than its beat count")
	}
	out := make([]Beat, n)
	for i := range out {
		e := body[8*i:]
		out[i] = Beat{Number: int(binary.BigEndian.Uint16(e[0:2])),
			BPM:  float64(binary.BigEndian.Uint16(e[2:4])) / 100,
			Time: float64(binary.BigEndian.Uint32(e[4:8])) / 1000}
	}
	return out, nil
}

// unmaskPSSI restores a masked PSSI tag.
func unmaskPSSI(tag []byte) []byte {
	n := binary.BigEndian.Uint16(tag[16:18])
	out := append([]byte(nil), tag...)
	for i := pssiMaskFrom; i < len(out); i++ {
		out[i] ^= pssiMask[(i-pssiMaskFrom)%len(pssiMask)] + byte(n)
	}
	return out
}

// Phrases is an EXT file's PSSI structure; false if it has none. Masked or
// not is decided by which reading gives a valid mood.
func Phrases(ext []byte) (Structure, bool, error) {
	t, err := sections(ext)
	if err != nil {
		return Structure{}, false, err
	}
	s, ok := t["PSSI"]
	if !ok {
		return Structure{}, false, nil
	}
	raw := ext[s.off : s.off+s.length]
	for _, tag := range [][]byte{unmaskPSSI(raw), raw} {
		entry := binary.BigEndian.Uint32(tag[12:16])
		n := int(binary.BigEndian.Uint16(tag[16:18]))
		mood := int(binary.BigEndian.Uint16(tag[18:20]))
		name, valid := moods[mood]
		if !valid || entry != 24 || s.header+24*n > len(tag) {
			continue
		}
		st := Structure{Mood: name, EndBeat: int(binary.BigEndian.Uint16(tag[26:28]))}
		for i := range n {
			e := tag[s.header+24*i:]
			kind := int(binary.BigEndian.Uint16(e[4:6]))
			label, known := kinds[mood][kind]
			if !known {
				label = fmt.Sprintf("kind%d", kind)
			}
			st.Phrases = append(st.Phrases, Phrase{Beat: int(binary.BigEndian.Uint16(e[2:4])), Kind: label})
		}
		return st, true, nil
	}
	return Structure{}, false, errors.New("rekordbox anlz: PSSI tag with no valid mood")
}

// Analysis is one track's grid and phrases.
type Analysis struct {
	Beats   []Beat
	Phrases *Structure // nil when Rekordbox did no phrase analysis
}

// ReadUSB reads every track's analysis under usb (the folder holding
// PIONEER), keyed by its audio path on the USB as PPTH gives it
// ("/Contents/...").
func ReadUSB(usb string) (map[string]Analysis, error) {
	dats, err := filepath.Glob(filepath.Join(usb, "PIONEER", "USBANLZ", "*", "*", "ANLZ0000.DAT"))
	if err != nil {
		return nil, err
	}
	out := map[string]Analysis{}
	for _, dat := range dats {
		d, err := os.ReadFile(dat)
		if err != nil {
			return nil, err
		}
		path, err := AudioPath(d)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dat, err)
		}
		beats, err := Beats(d)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dat, err)
		}
		a := Analysis{Beats: beats}
		if e, err := os.ReadFile(strings.TrimSuffix(dat, ".DAT") + ".EXT"); err == nil {
			st, ok, err := Phrases(e)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", dat, err)
			}
			if ok {
				a.Phrases = &st
			}
		}
		out[path] = a
	}
	return out, nil
}
