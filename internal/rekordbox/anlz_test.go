package rekordbox

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

// anlzFile builds an ANLZ file from tagged sections, as Rekordbox lays one
// out: a 28-byte PMAI header, then each section.
func anlzFile(parts ...[]byte) []byte {
	out := make([]byte, 28)
	copy(out, "PMAI")
	binary.BigEndian.PutUint32(out[4:], 28)
	for _, s := range parts {
		out = append(out, s...)
	}
	binary.BigEndian.PutUint32(out[8:], uint32(len(out)))
	return out
}

func tagged(fourcc string, header []byte, body []byte) []byte {
	out := make([]byte, 12)
	copy(out, fourcc)
	binary.BigEndian.PutUint32(out[4:], uint32(12+len(header)))
	binary.BigEndian.PutUint32(out[8:], uint32(12+len(header)+len(body)))
	return append(append(out, header...), body...)
}

func ppth(path string) []byte {
	u := utf16.Encode([]rune(path + "\x00"))
	body := make([]byte, 2*len(u))
	for i, c := range u {
		binary.BigEndian.PutUint16(body[2*i:], c)
	}
	h := make([]byte, 4)
	binary.BigEndian.PutUint32(h, uint32(len(body)))
	return tagged("PPTH", h, body)
}

func pqtz(beats []Beat) []byte {
	h := make([]byte, 12)
	binary.BigEndian.PutUint32(h[4:], 0x80000)
	binary.BigEndian.PutUint32(h[8:], uint32(len(beats)))
	var body []byte
	for _, b := range beats {
		e := make([]byte, 8)
		binary.BigEndian.PutUint16(e[0:], uint16(b.Number))
		binary.BigEndian.PutUint16(e[2:], uint16(b.BPM*100+0.5))
		binary.BigEndian.PutUint32(e[4:], uint32(b.Time*1000+0.5))
		body = append(body, e...)
	}
	return tagged("PQTZ", h, body)
}

// pssi builds a PSSI tag of the given mood and phrases (kinds by number),
// masked as Rekordbox 6 writes it when mask is true.
func pssi(mood, endBeat int, phrases [][2]int, mask bool) []byte {
	h := make([]byte, 20)
	binary.BigEndian.PutUint32(h[0:], 24)
	binary.BigEndian.PutUint16(h[4:], uint16(len(phrases)))
	binary.BigEndian.PutUint16(h[6:], uint16(mood))
	binary.BigEndian.PutUint16(h[14:], uint16(endBeat))
	var body []byte
	for i, p := range phrases {
		e := make([]byte, 24)
		binary.BigEndian.PutUint16(e[0:], uint16(i+1))
		binary.BigEndian.PutUint16(e[2:], uint16(p[0]))
		binary.BigEndian.PutUint16(e[4:], uint16(p[1]))
		body = append(body, e...)
	}
	tag := tagged("PSSI", h, body)
	if mask {
		tag = unmaskPSSI(tag) // XOR: masking and unmasking are the same step
	}
	return tag
}

func TestBeatsPathAndMaskedPhrasesRoundTrip(t *testing.T) {
	beats := []Beat{{2, 128, 0.25}, {3, 128, 0.719}, {4, 128, 1.188}, {1, 128.5, 1.656}}
	dat := anlzFile(ppth("/Contents/Artist/Album/Song.mp3"), pqtz(beats))
	if p, err := AudioPath(dat); err != nil || p != "/Contents/Artist/Album/Song.mp3" {
		t.Errorf("AudioPath = %q, %v", p, err)
	}
	got, err := Beats(dat)
	if err != nil || len(got) != 4 || got[3] != beats[3] || got[0] != beats[0] {
		t.Fatalf("Beats = %+v, %v", got, err)
	}
	for _, mask := range []bool{true, false} {
		ext := anlzFile(ppth("/x.mp3"), pssi(1, 260, [][2]int{{1, 1}, {33, 2}, {65, 5}, {129, 3}, {161, 5}, {225, 6}}, mask))
		st, ok, err := Phrases(ext)
		if err != nil || !ok {
			t.Fatalf("mask %v: Phrases ok %v err %v", mask, ok, err)
		}
		kinds := []string{"intro", "up", "chorus", "down", "chorus", "outro"}
		if st.Mood != "high" || st.EndBeat != 260 || len(st.Phrases) != len(kinds) {
			t.Fatalf("mask %v: %+v", mask, st)
		}
		for i, k := range kinds {
			if st.Phrases[i].Kind != k {
				t.Errorf("mask %v: phrase %d = %+v, want %s", mask, i, st.Phrases[i], k)
			}
		}
		if st.Phrases[1].Beat != 33 {
			t.Errorf("mask %v: phrase 2 starts on beat %d, want 33", mask, st.Phrases[1].Beat)
		}
	}
	if _, ok, err := Phrases(anlzFile(ppth("/x.mp3"))); ok || err != nil {
		t.Errorf("a file without PSSI: ok %v err %v", ok, err)
	}
	if _, err := Beats([]byte("not an anlz file")); err == nil {
		t.Error("garbage parsed")
	}
}

func TestReadUSBPairsEachTracksFiles(t *testing.T) {
	usb := t.TempDir()
	dir := filepath.Join(usb, "PIONEER", "USBANLZ", "P001", "0000ABCD")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ANLZ0000.DAT", anlzFile(ppth("/Contents/A/B/c.mp3"), pqtz([]Beat{{1, 124, 0.1}, {2, 124, 0.584}})))
	write("ANLZ0000.EXT", anlzFile(ppth("/Contents/A/B/c.mp3"), pssi(2, 2, [][2]int{{1, 1}, {2, 9}}, true)))
	got, err := ReadUSB(usb)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := got["/Contents/A/B/c.mp3"]
	if !ok || len(a.Beats) != 2 || a.Phrases == nil || a.Phrases.Mood != "mid" || a.Phrases.Phrases[1].Kind != "chorus" {
		t.Fatalf("ReadUSB = %+v", got)
	}
}
