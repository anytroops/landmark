package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/fingerprint"
	"github.com/anytroops/landmark/internal/hybrid"
	"github.com/anytroops/landmark/internal/tracklist"
)

// clipSeconds is how much audio a query uses, as one tracklisting window.
const clipSeconds = 10.0

// verdict is one search family's answer for a clip.
type verdict struct {
	song
	Matched  bool    `json:"matched"`
	Search   string  `json:"search"`    // which search answered
	Score    float64 `json:"score"`     // matching landmarks at the best offset
	RunnerUp float64 `json:"runner_up"` // best score outside the answer's duplicate cluster
	TempoPct float64 `json:"tempo_pct"` // the clip plays the song this much faster
	OffsetS  float64 `json:"offset_s"`  // where in the song the clip starts
}

// identifyResponse is POST /api/identify's answer.
type identifyResponse struct {
	Answer   *verdict `json:"answer"`   // the hybrid's: the best accepted search
	Pairs    verdict  `json:"pairs"`    // the pair fingerprint alone, as a classic system would
	Triplets verdict  `json:"triplets"` // the tempo-invariant fingerprint, the clip as is
	Seconds  float64  `json:"seconds"`  // clip length
	Ms       struct {
		Decode float64 `json:"decode"`
		Search float64 `json:"search"`
	} `json:"ms"`
	Truth *truth `json:"truth,omitempty"`
}

// truth is what an example clip really is.
type truth struct {
	song
	TempoPct float64 `json:"tempo_pct"`
	Kind     string  `json:"kind"`
	StartS   float64 `json:"start_s"`
	Correct  bool    `json:"correct"` // the answer names it (or a duplicate of it)
}

// identify answers a clip: an uploaded file ("audio", from "start"
// seconds), or an example song ("example": dense id or "random") played
// "tempo" percent fast like a turntable or with keylock ("kind").
func (s *server) identify(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		httpError(w, http.StatusBadRequest, "read the form: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	start, _ := strconv.ParseFloat(r.FormValue("start"), 64)
	tempo, _ := strconv.ParseFloat(r.FormValue("tempo"), 64)
	if math.Abs(tempo) > 30 {
		httpError(w, http.StatusBadRequest, "tempo change %v%% is out of range", tempo)
		return
	}
	var resp identifyResponse
	var path, filter string
	inSeconds := clipSeconds
	if ex := r.FormValue("example"); ex != "" {
		dense, err := s.exampleSong(ex)
		if err != nil {
			httpError(w, http.StatusBadRequest, "%v", err)
			return
		}
		path, _ = s.cat.Audio(dense, s.cfg.fmaDir)
		if r.FormValue("start") == "" {
			start = 5
		}
		kind := r.FormValue("kind")
		filter, inSeconds = tempoFilter(tempo, kind)
		resp.Truth = &truth{song: s.song(dense), TempoPct: tempo, Kind: kind, StartS: start}
	} else {
		f, _, err := r.FormFile("audio")
		if err != nil {
			httpError(w, http.StatusBadRequest, "send an audio file or pick an example")
			return
		}
		defer func() { _ = f.Close() }()
		tmp, err := os.CreateTemp("", "landmark-demo-*")
		if err != nil {
			httpError(w, http.StatusInternalServerError, "%v", err)
			return
		}
		defer func() { _ = os.Remove(tmp.Name()) }()
		_, err = io.Copy(tmp, f)
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			httpError(w, http.StatusInternalServerError, "store the upload: %v", err)
			return
		}
		path = tmp.Name()
	}

	t0 := time.Now()
	x, err := audio.DecodeExcerpt(ctx, path, audio.SampleRate, start, inSeconds, filter)
	if err != nil {
		httpError(w, http.StatusBadRequest, "decode the audio: %v", err)
		return
	}
	x = x[:min(len(x), int(clipSeconds*audio.SampleRate))]
	resp.Seconds = float64(len(x)) / audio.SampleRate
	if resp.Seconds < 3 {
		httpError(w, http.StatusBadRequest, "only %.1f s of audio: send at least 3 s", resp.Seconds)
		return
	}
	t1 := time.Now()
	fp := s.fps.Get().(*hybrid.Fingerprinters)
	rows, _, err := s.search.Window(ctx, x, 0, fp)
	s.fps.Put(fp)
	if err != nil {
		httpError(w, http.StatusBadGateway, "search: %v", err)
		return
	}
	resp.Ms.Decode = ms(t1.Sub(t0))
	resp.Ms.Search = ms(time.Since(t1))
	resp.Pairs = s.verdict(best(rows, func(r tracklist.Row) bool { return r.Mode == "none" }))
	resp.Triplets = s.verdict(best(rows, func(r tracklist.Row) bool { return r.Mode == "b2" }))
	if b := best(rows, accepted); b != nil {
		v := s.verdict(b)
		resp.Answer = &v
	}
	if resp.Truth != nil && resp.Answer != nil {
		resp.Truth.Correct = s.cat.Cluster(uint32(resp.Answer.Track)) == s.cat.Cluster(uint32(resp.Truth.Track))
	}
	writeJSON(w, resp)
}

// exampleSong resolves "random" or a dense id to an example song.
func (s *server) exampleSong(ex string) (int, error) {
	if len(s.songs) == 0 {
		return 0, fmt.Errorf("no example songs: is %s there?", s.cfg.fmaDir)
	}
	if ex == "random" {
		return s.songs[rand.IntN(len(s.songs))], nil //nolint:gosec // a demo pick, not security
	}
	dense, err := strconv.Atoi(ex)
	if err != nil || dense < 0 || dense >= len(s.cat.Tracks) {
		return 0, fmt.Errorf("unknown example %q", ex)
	}
	if _, ok := s.cat.Audio(dense, s.cfg.fmaDir); !ok {
		return 0, fmt.Errorf("no audio for track %d", dense)
	}
	return dense, nil
}

// tempoFilter is the ffmpeg filter that plays audio pct percent fast, like
// a turntable (pitch moves with it) or with keylock (pitch kept), and how
// many input seconds make clipSeconds of output.
func tempoFilter(pct float64, kind string) (string, float64) {
	f := 1 + pct/100
	if pct == 0 {
		return "", clipSeconds
	}
	in := clipSeconds * f
	if kind == "keylock" {
		return fmt.Sprintf("atempo=%.4f", f), in
	}
	const sr = audio.SampleRate
	return fmt.Sprintf("aresample=%d,asetrate=%.2f,aresample=%d", sr, sr*f, sr), in
}

// accepted is the tracklister's window rule (EVAL; the hybrid's
// calibration): weighted score at least tAbs and tRatio times the
// runner-up.
func accepted(r tracklist.Row) bool {
	return r.Track >= 0 && r.Score >= tAbs && r.Score >= tRatio*r.RunnerUp
}

// best returns the highest-scoring row that keep accepts, or nil.
func best(rows []tracklist.Row, keep func(tracklist.Row) bool) *tracklist.Row {
	var b *tracklist.Row
	for i := range rows {
		if r := &rows[i]; r.Track >= 0 && keep(*r) && (b == nil || r.Score > b.Score) {
			b = r
		}
	}
	return b
}

func (s *server) verdict(r *tracklist.Row) verdict {
	if r == nil {
		return verdict{song: song{Track: -1}, Search: "no match"}
	}
	weight := 1.0
	if isPair(r.Mode) {
		weight = alpha // show the pair search's own votes
	}
	return verdict{song: s.song(r.Track), Matched: accepted(*r), Search: searchName(r.Mode, r.Factor),
		Score: r.Score / weight, RunnerUp: r.RunnerUp / weight, TempoPct: 100 * (r.Factor - 1),
		OffsetS: r.Offset * float64(fingerprint.DefaultParams().Hop) / audio.SampleRate}
}

func isPair(mode string) bool { return mode == "none" || mode == "keylock" }

func searchName(mode string, factor float64) string {
	switch mode {
	case "b2":
		return "triplets"
	case "b2_speed":
		if factor > 1.4 {
			return "triplets, double time"
		}
		return "triplets, half time"
	case "none":
		return "pairs"
	case "keylock":
		if factor > 1.4 {
			return "pairs, keylock double time"
		}
		return "pairs, keylock half time"
	}
	return mode
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
