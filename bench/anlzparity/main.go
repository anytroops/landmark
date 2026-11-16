// Command anlzparity writes what internal/rekordbox reads from a Rekordbox
// USB export's analysis files (every song's beat grid and phrases) as JSON,
// for prototype/anlz.py to compare against (bench/anlzparity/compare.py).
//
//	go run ./bench/anlzparity -usb ~/Desktop > /tmp/anlz_go.json
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/anytroops/landmark/internal/rekordbox"
)

type song struct {
	Beats   [][3]float64 `json:"beats"` // number, bpm, time
	Mood    string       `json:"mood,omitempty"`
	EndBeat int          `json:"end_beat,omitempty"`
	Phrases [][2]any     `json:"phrases,omitempty"` // beat, kind
}

func main() {
	usb := flag.String("usb", "", "the folder holding PIONEER")
	flag.Parse()
	lib, err := rekordbox.ReadUSB(*usb)
	if err != nil {
		log.Fatal(err)
	}
	out := make(map[string]song, len(lib))
	for path, a := range lib {
		s := song{Beats: make([][3]float64, len(a.Beats))}
		for i, b := range a.Beats {
			s.Beats[i] = [3]float64{float64(b.Number), b.BPM, b.Time}
		}
		if a.Phrases != nil {
			s.Mood, s.EndBeat = a.Phrases.Mood, a.Phrases.EndBeat
			for _, p := range a.Phrases.Phrases {
				s.Phrases = append(s.Phrases, [2]any{p.Beat, p.Kind})
			}
		}
		out[path] = s
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		log.Fatal(err)
	}
}
