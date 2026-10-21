// Command streamserver plays playlists as live audio streams over HTTP, for
// testing live monitoring (docs/design/live-monitoring.md) with known
// ground truth. It is a test tool, not part of the service.
//
//	streamserver -stations stations.json -schedule schedule.jsonl
//
// stations.json is [{"id": "st01", "files": ["a.wav", "b.wav"]}]. Every
// station starts broadcasting at -start (Unix seconds; default a few
// seconds after launch) and plays its files back to back in real time,
// like a radio station: a client of /stream/<id> hears the station from
// the moment it connects, as 32-bit float mono WAV at 11,025 Hz (the
// samples audio.Decode gives for each file, so a client decodes exactly the
// offline samples). The X-Landmark-Start header gives the broadcast time
// of the first sample sent. The schedule file gets one JSON line per file
// played: station, file, and its broadcast start and end.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/monitor"
)

func main() {
	stationsPath := flag.String("stations", "", "stations JSON (required)")
	schedulePath := flag.String("schedule", "", "write the broadcast schedule here (JSON lines)")
	listen := flag.String("listen", ":7800", "HTTP listen address")
	start := flag.Float64("start", 0, "broadcast start, Unix seconds (0: 3 s from now)")
	loop := flag.Bool("loop", false, "repeat each playlist")
	flag.Parse()
	if *stationsPath == "" {
		fmt.Fprintln(os.Stderr, "usage: streamserver -stations stations.json [flags]")
		os.Exit(2)
	}
	if *start == 0 {
		*start = float64(time.Now().Add(3*time.Second).UnixNano()) / 1e9
	}
	if err := run(*stationsPath, *schedulePath, *listen, *start, *loop); err != nil {
		slog.Error("streamserver failed", "err", err)
		os.Exit(1)
	}
}

type stationConfig struct {
	ID    string   `json:"id"`
	Files []string `json:"files"`
}

func run(stationsPath, schedulePath, listen string, start float64, loop bool) error {
	b, err := os.ReadFile(stationsPath)
	if err != nil {
		return err
	}
	var cfgs []stationConfig
	if err := json.Unmarshal(b, &cfgs); err != nil {
		return fmt.Errorf("parse %s: %w", stationsPath, err)
	}
	sched := io.Discard
	if schedulePath != "" {
		f, err := os.Create(schedulePath)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		sched = f
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s := newServer(start, &lockedWriter{w: sched})
	for _, c := range cfgs {
		st := s.add(c.ID)
		go func() {
			if err := st.broadcast(ctx, c.Files, loop); err != nil && ctx.Err() == nil {
				slog.Error("station failed", "station", c.ID, "err", err)
			}
		}()
	}
	srv := &http.Server{Addr: listen, Handler: s.handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("broadcasting", "stations", len(cfgs), "listen", listen, "start", start)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

type server struct {
	start    float64 // broadcast start, Unix seconds
	sched    io.Writer
	stations map[string]*station
	now      func() float64
}

func newServer(start float64, sched io.Writer) *server {
	return &server{start: start, sched: sched, stations: map[string]*station{},
		now: func() float64 { return float64(time.Now().UnixNano()) / 1e9 }}
}

func (s *server) add(id string) *station {
	st := &station{id: id, srv: s, clients: map[chan []byte]bool{}}
	s.stations[id] = st
	return st
}

// station broadcasts one playlist to its current listeners.
type station struct {
	id  string
	srv *server

	mu      sync.Mutex
	pos     int // samples broadcast so far
	clients map[chan []byte]bool
}

// broadcast plays the files in real time from the server's start time.
func (st *station) broadcast(ctx context.Context, files []string, loop bool) error {
	for {
		for _, f := range files {
			pcm, err := audio.Decode(ctx, f, audio.SampleRate)
			if err != nil {
				return err
			}
			st.mu.Lock()
			begin := st.srv.start + float64(st.pos)/audio.SampleRate
			st.mu.Unlock()
			line, _ := json.Marshal(map[string]any{"station": st.id, "file": f, "start_unix": begin,
				"end_unix": begin + float64(len(pcm))/audio.SampleRate})
			if _, err := st.srv.sched.Write(append(line, '\n')); err != nil {
				return err
			}
			for off := 0; off < len(pcm); {
				st.mu.Lock()
				due := int((st.srv.now()-st.srv.start)*audio.SampleRate) - st.pos
				st.mu.Unlock()
				if due <= 0 {
					select {
					case <-ctx.Done():
						return nil
					case <-time.After(50 * time.Millisecond):
					}
					continue
				}
				n := min(due, len(pcm)-off)
				st.send(pcm[off : off+n])
				off += n
			}
		}
		if !loop {
			st.close()
			return nil
		}
	}
}

// send fans samples out; a listener more than its buffer behind is
// dropped, as a radio server would.
func (st *station) send(x []float32) {
	b := make([]byte, 4*len(x))
	for i, v := range x {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(v))
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.pos += len(x)
	for c := range st.clients {
		select {
		case c <- b:
		default:
			close(c)
			delete(st.clients, c)
		}
	}
}

func (st *station) close() {
	st.mu.Lock()
	defer st.mu.Unlock()
	for c := range st.clients {
		close(c)
	}
	st.clients = nil
}

// join adds a listener and returns its channel and the broadcast time of
// the first sample it will get.
func (st *station) join() (chan []byte, float64, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.clients == nil {
		return nil, 0, false // playlist over
	}
	c := make(chan []byte, 256)
	st.clients[c] = true
	return c, st.srv.start + float64(st.pos)/audio.SampleRate, true
}

func (st *station) leave(c chan []byte) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.clients[c] {
		delete(st.clients, c)
	}
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stream/{id}", func(w http.ResponseWriter, r *http.Request) {
		st, ok := s.stations[r.PathValue("id")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		c, first, ok := st.join()
		if !ok {
			http.Error(w, "station off the air", http.StatusGone)
			return
		}
		defer st.leave(c)
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set(monitor.StartHeader, strconv.FormatFloat(first, 'f', 6, 64))
		flusher, _ := w.(http.Flusher)
		if _, err := w.Write(wavHeader()); err != nil {
			return
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case b, ok := <-c:
				if !ok {
					return
				}
				if _, err := w.Write(b); err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	})
	return mux
}

// wavHeader starts an unbounded 32-bit float mono WAV stream.
func wavHeader() []byte {
	const rate, bytesPerSample = audio.SampleRate, 4
	b := make([]byte, 0, 44)
	le := binary.LittleEndian
	b = append(b, "RIFF"...)
	b = le.AppendUint32(b, 0xFFFFFFFF)
	b = append(b, "WAVEfmt "...)
	b = le.AppendUint32(b, 16)
	b = le.AppendUint16(b, 3) // IEEE float
	b = le.AppendUint16(b, 1) // mono
	b = le.AppendUint32(b, rate)
	b = le.AppendUint32(b, rate*bytesPerSample)
	b = le.AppendUint16(b, bytesPerSample)
	b = le.AppendUint16(b, 8*bytesPerSample)
	b = append(b, "data"...)
	return le.AppendUint32(b, 0xFFFFFFFF)
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
