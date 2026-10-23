package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/catalog"
	"github.com/anytroops/landmark/internal/tracklist"
	"github.com/anytroops/landmark/internal/triplet"
)

// SongReport is how one tracklist entry was mixed in (D43).
type SongReport struct {
	FadeIn, FadeOut []float64 // mix seconds, start and end; nil if not fitted
	Turntable       bool      // the tempo change moved the pitch too
	Rate            float64   // playback rate (reference time per mix time)
	BPM             float64   // the song's tempo in the set; 0 if its reference BPM is unknown
}

// Mix estimates every tracklist entry's fades and kind of tempo change from
// its reference audio (found through cat, FMA tracks under fmaDir), and its
// BPM in the set from bpm (reference BPM by dense id), keyed by entry
// index. Entries whose reference audio or triplet rows are missing are left
// out.
func Mix(ctx context.Context, pcm []float32, rows []tracklist.Row, entries []tracklist.Entry,
	cat *catalog.Catalog, fmaDir string, bpm map[int]float64) (map[int]SongReport, error) {
	fp := triplet.New(triplet.DefaultParams())
	mix := Power(fp.Spectrogram(pcm))
	var songs []Song
	var entryOf []int
	for i, e := range entries {
		a, rate, ok := Alignment(rows, e.Track, e.Start, e.End)
		path, known := cat.Audio(e.Track, fmaDir)
		if !ok || !known {
			continue
		}
		ref, err := audio.Decode(ctx, path, audio.SampleRate)
		if errors.Is(err, audio.ErrDecode) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reference of track %d: %w", e.Track, err)
		}
		songs = append(songs, Song{Track: e.Track, Start: e.Start, End: e.End, A: a, Rate: rate,
			Ref: Power(fp.Spectrogram(ref))})
		entryOf = append(entryOf, i)
	}
	out := map[int]SongReport{}
	for _, r := range Analyze(mix, songs) {
		s := SongReport{Turntable: r.Turntable, Rate: r.Rate}
		if r.FadeInOK {
			s.FadeIn = r.FadeIn[:]
		}
		if r.FadeOutOK {
			s.FadeOut = r.FadeOut[:]
		}
		if b, ok := bpm[r.Track]; ok {
			s.BPM = b * r.Rate
		}
		out[entryOf[r.Song]] = s
	}
	return out, nil
}

// ReadBPM reads an index's bpm.json (dense id -> reference BPM, written by
// eval.dj_bpm), if any.
func ReadBPM(dir string) (map[int]float64, error) {
	b, err := os.ReadFile(filepath.Join(dir, "bpm.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var raw map[string]float64
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse bpm.json: %w", err)
	}
	out := make(map[int]float64, len(raw))
	for k, v := range raw {
		d, err := strconv.Atoi(k)
		if err != nil {
			return nil, fmt.Errorf("bpm.json: bad track id %q", k)
		}
		out[d] = v
	}
	return out, nil
}
