package main

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/anytroops/landmark/internal/analysis"
	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/hybrid"
	"github.com/anytroops/landmark/internal/tracklist"
)

// maxMixSeconds bounds an uploaded mix (a long DJ set is fine).
const maxMixSeconds = 3 * 3600

// entryOut is one tracklist entry as the page shows it.
type entryOut struct {
	song
	StartS   float64 `json:"start_s"`
	EndS     float64 `json:"end_s"`
	TempoPct float64 `json:"tempo_pct"`
	Evidence float64 `json:"evidence"`
	Resumed  bool    `json:"resumed,omitempty"`
	Correct  *bool   `json:"correct,omitempty"` // for an example mix: it names a song playing then
}

// truthOut is one ground-truth segment of an example mix.
type truthOut struct {
	song
	Known    bool    `json:"known"` // in the catalog
	StartS   float64 `json:"start_s"`
	EndS     float64 `json:"end_s"`
	FadeIn   float64 `json:"fade_in"`
	FadeOut  float64 `json:"fade_out"`
	TempoPct float64 `json:"tempo_pct"`
	Mode     string  `json:"mode"` // none, resample (turntable), stretch (keylock)
	Found    bool    `json:"found"`
}

// tracklist streams a mix's tracklist as JSON lines while its windows are
// searched: "start", one "window" per window in order, "song" events from
// the online decoder (D38) as each becomes final, then "analysis" (D43)
// and "done". The input is an example mix ("example": its index) or an
// uploaded file ("audio").
func (s *server) tracklist(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httpError(w, http.StatusBadRequest, "read the form: %v", err)
		return
	}
	select {
	case s.jobs <- struct{}{}:
		defer func() { <-s.jobs }()
	default:
		httpError(w, http.StatusServiceUnavailable, "two mixes are being tracklisted already; try again shortly")
		return
	}
	ctx := r.Context()
	var path string
	var ex *mix
	if v := r.FormValue("example"); v != "" {
		i, err := strconv.Atoi(v)
		if err != nil || i < 0 || i >= len(s.mixes) {
			httpError(w, http.StatusBadRequest, "unknown example mix %q", v)
			return
		}
		ex, path = &s.mixes[i], s.mixes[i].File
	} else {
		f, _, err := r.FormFile("audio")
		if err != nil {
			httpError(w, http.StatusBadRequest, "send a mix or pick an example")
			return
		}
		defer func() { _ = f.Close() }()
		tmp, err := os.CreateTemp("", "landmark-demo-mix-*")
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
	pcm, err := audio.Decode(ctx, path, audio.SampleRate)
	if err != nil {
		httpError(w, http.StatusBadRequest, "decode the mix: %v", err)
		return
	}
	if len(pcm) > maxMixSeconds*audio.SampleRate {
		httpError(w, http.StatusBadRequest, "the mix is over %d hours", maxMixSeconds/3600)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	out := &lineWriter{w: w}
	s.runTracklist(ctx, pcm, ex, out)
}

// lineWriter writes JSON lines and flushes each one.
type lineWriter struct {
	w   http.ResponseWriter
	err error
}

func (l *lineWriter) send(v any) {
	if l.err != nil {
		return
	}
	b, err := json.Marshal(v)
	if err == nil {
		_, err = l.w.Write(append(b, '\n'))
	}
	if err == nil {
		if f, ok := l.w.(http.Flusher); ok {
			f.Flush()
		}
	}
	l.err = err
}

// windowResult is one window's searches.
type windowResult struct {
	rows []tracklist.Row
	err  error
}

func (s *server) runTracklist(ctx context.Context, pcm []float32, ex *mix, out *lineWriter) {
	began := time.Now()
	size := int(window * audio.SampleRate)
	step := int(math.Floor(hop * audio.SampleRate)) // 27,562 samples: cmd/tracklist's window starts
	var starts []int
	for a := 0; a+size <= len(pcm); a += step {
		starts = append(starts, a)
	}
	start := map[string]any{"type": "start", "seconds": float64(len(pcm)) / audio.SampleRate,
		"windows": len(starts), "window_s": window, "hop_s": hop}
	var truth []truthOut
	if ex != nil {
		truth = s.truth(ex)
		start["name"], start["truth"] = ex.Name, truth
		start["timescale"], start["effect"] = ex.Timescale, ex.Effect
	}
	out.send(start)

	// Search windows in parallel, report them in order.
	results := make([]chan windowResult, len(starts))
	for i := range results {
		results[i] = make(chan windowResult, 1)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range max(s.cfg.workers, 1) {
		wg.Go(func() {
			fp := s.fps.Get().(*hybrid.Fingerprinters)
			defer s.fps.Put(fp)
			for i := range jobs {
				t0 := hybrid.WindowT0(starts[i])
				rows, _, err := s.search.Window(ctx, pcm[starts[i]:starts[i]+size], t0, fp)
				results[i] <- windowResult{rows: rows, err: err}
			}
		})
	}
	go func() {
		defer close(jobs)
		for i := range starts {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	defer wg.Wait()

	dec := tracklist.NewOnline(window, tAbs, tRatio, smoothK, mergeGap)
	var all []tracklist.Row
	var entries []tracklist.Entry
	open := -1 // index of the entry whose start is sent and end is not
	emit := func(ev []tracklist.Event) {
		for _, e := range ev {
			if e.Kind == tracklist.Start {
				open = len(entries)
				entries = append(entries, e.Entry)
				out.send(map[string]any{"type": "song", "kind": "start", "index": open, "entry": s.entry(e.Entry, false, truth)})
				continue
			}
			entries[open] = e.Entry
			out.send(map[string]any{"type": "song", "kind": "end", "index": open, "entry": s.entry(e.Entry, e.Resumed, truth)})
		}
	}
	for i := range starts {
		var res windowResult
		select {
		case res = <-results[i]:
		case <-ctx.Done():
			return
		}
		t0 := hybrid.WindowT0(starts[i])
		rows := res.rows
		if res.err != nil || len(rows) == 0 {
			rows = []tracklist.Row{{T0: t0, Mode: "b2", Factor: 1, Track: -1}}
		}
		all = append(all, rows...)
		msg := map[string]any{"type": "window", "i": i, "t0": t0}
		if res.err != nil {
			msg["error"] = res.err.Error()
		}
		if b := best(rows, func(tracklist.Row) bool { return true }); b != nil {
			msg["track"], msg["score"], msg["accepted"] = b.Track, b.Score, accepted(*b)
			msg["tempo_pct"], msg["search"] = 100*(b.Factor-1), searchName(b.Mode, b.Factor)
			if accepted(*b) {
				msg["song"] = s.song(b.Track)
			}
		}
		out.send(msg)
		ev, err := dec.Push(rows)
		if err != nil {
			out.send(map[string]any{"type": "error", "error": err.Error()})
			return
		}
		emit(ev)
	}
	emit(dec.Close())
	if out.err != nil {
		return
	}

	found := 0
	for _, t := range truth {
		if t.Found {
			found++
		}
	}
	done := map[string]any{"type": "done", "entries": len(entries), "searches": len(all),
		"elapsed_s": time.Since(began).Seconds()}
	if ex != nil {
		correct := 0
		for _, e := range entries {
			if s.overlapsTruth(e, truth) {
				correct++
			}
		}
		known := 0
		for _, t := range truth {
			if t.Known {
				known++
			}
		}
		done["found"], done["known"], done["correct"] = found, known, correct
		done["truth"] = truth // now with each segment's found flag
	}
	out.send(map[string]any{"type": "analyzing"})
	tracklist.SortRows(all)
	if reports, err := analysis.Mix(ctx, pcm, all, entries, s.cat, s.cfg.fmaDir, s.bpm); err == nil {
		songs := map[string]any{}
		for i, a := range reports {
			songs[strconv.Itoa(i)] = map[string]any{"fade_in": a.FadeIn, "fade_out": a.FadeOut,
				"turntable": a.Turntable, "rate": a.Rate, "bpm": a.BPM}
		}
		out.send(map[string]any{"type": "analysis", "songs": songs})
	} else {
		out.send(map[string]any{"type": "analysis", "error": err.Error()})
	}
	done["elapsed_s"] = time.Since(began).Seconds()
	out.send(done)
}

// entry converts a decoder entry for the page, marking it against truth
// when there is one (and marking the truth segments it finds).
func (s *server) entry(e tracklist.Entry, resumed bool, truth []truthOut) entryOut {
	out := entryOut{song: s.song(e.Track), StartS: e.Start, EndS: e.End, TempoPct: 100 * (e.Factor - 1),
		Evidence: e.Evidence, Resumed: resumed}
	if truth != nil {
		ok := s.overlapsTruth(e, truth)
		out.Correct = &ok
		for i := range truth {
			if t := &truth[i]; t.Known && s.sameSong(e.Track, t.Track) && e.Start < t.EndS && e.End > t.StartS {
				t.Found = true
			}
		}
	}
	return out
}

// overlapsTruth is eval.tracklist_score's rule for a correct entry: it
// overlaps a known segment of the same song (or a duplicate of it).
func (s *server) overlapsTruth(e tracklist.Entry, truth []truthOut) bool {
	return slices.ContainsFunc(truth, func(t truthOut) bool {
		return t.Known && s.sameSong(e.Track, t.Track) && e.Start < t.EndS && e.End > t.StartS
	})
}

func (s *server) sameSong(a, b int) bool {
	return a >= 0 && b >= 0 && s.cat.Cluster(uint32(a)) == s.cat.Cluster(uint32(b))
}

func (s *server) truth(m *mix) []truthOut {
	out := make([]truthOut, len(m.Segments))
	for i, g := range m.Segments {
		out[i] = truthOut{song: s.song(g.Dense), Known: g.Known, StartS: g.MixStart, EndS: g.MixEnd,
			FadeIn: g.FadeIn, FadeOut: g.FadeOut, TempoPct: g.Pct, Mode: g.Mode}
	}
	return out
}
