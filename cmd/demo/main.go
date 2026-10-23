// Command demo serves a web page that shows Landmark working on this
// machine: identify a clip (a dropped file, the microphone, or a test song
// with a tempo change), tracklist a DJ mix while its windows are searched,
// and watch live monitoring's stations. It searches the triplet and pair
// clusters as cmd/tracklist does (the hybrid, D36) and, with -registry,
// follows live monitoring's event logs (D40).
//
//	make demo    # starts the clusters, live stations, and this server
//
//	demo -coordinator 127.0.0.1:7600 -index data/segments/medium_unmixdb_b2 \
//	    -pairs-coordinator 127.0.0.1:7700 -pairs-index data/segments/medium_unmixdb \
//	    -registry 127.0.0.1:7900
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/analysis"
	"github.com/anytroops/landmark/internal/catalog"
	"github.com/anytroops/landmark/internal/hybrid"
)

//go:embed web
var web embed.FS

type config struct {
	listen                       string
	coordinator, index           string
	pairsCoordinator, pairsIndex string
	registry                     string
	titles, mixes, manifest      string
	sets                         string
	fmaDir                       string
	timeout                      time.Duration
	workers                      int
}

func main() {
	var cfg config
	flag.StringVar(&cfg.listen, "listen", "127.0.0.1:8080", "HTTP listen address")
	flag.StringVar(&cfg.coordinator, "coordinator", "", "coordinator of the triplet index (required)")
	flag.StringVar(&cfg.index, "index", "", "triplet index directory (required)")
	flag.StringVar(&cfg.pairsCoordinator, "pairs-coordinator", "", "coordinator of the pair index (required)")
	flag.StringVar(&cfg.pairsIndex, "pairs-index", "", "pair index directory (required)")
	flag.StringVar(&cfg.registry, "registry", "", "live monitoring registry (empty: no live tab)")
	flag.StringVar(&cfg.titles, "titles", "data/fma_medium_titles.json", "FMA id -> artist and title (optional)")
	flag.StringVar(&cfg.mixes, "mixes", "eval/mixes/unmixdb_s8.json", "example mixes with ground truth")
	flag.StringVar(&cfg.sets, "sets", "set044,set123,set230,set281", "which sets of -mixes to offer (the test sets)")
	flag.StringVar(&cfg.manifest, "manifest", "eval/manifests/splits_medium_seed0.json", "example songs: its test positives")
	flag.StringVar(&cfg.fmaDir, "fma-dir", "data/fma_medium", "FMA audio, for example songs and mix analysis")
	flag.DurationVar(&cfg.timeout, "timeout", 5*time.Second, "deadline per search")
	flag.IntVar(&cfg.workers, "workers", 4, "windows of a mix searched at once")
	flag.Parse()
	if cfg.coordinator == "" || cfg.index == "" || cfg.pairsCoordinator == "" || cfg.pairsIndex == "" {
		fmt.Fprintln(os.Stderr, "usage: demo -coordinator addr -index dir -pairs-coordinator addr -pairs-index dir [flags]")
		os.Exit(2)
	}
	if err := run(cfg); err != nil {
		slog.Error("demo failed", "err", err)
		os.Exit(1)
	}
}

func run(cfg config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, closeAll, err := newServer(cfg)
	if err != nil {
		return err
	}
	defer closeAll()
	srv := &http.Server{Addr: cfg.listen, Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("serving", "url", "http://"+cfg.listen, "tracks", len(s.cat.Tracks), "live", s.reg != nil)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// title is an FMA track's artist and title.
type title struct {
	Artist string `json:"artist"`
	Title  string `json:"title"`
}

// server holds what the handlers share. Searcher is safe for concurrent
// use; fingerprinters are not, so each request takes its own from fps.
type server struct {
	cfg    config
	search *hybrid.Searcher
	cat    *catalog.Catalog
	titles map[int]title
	bpm    map[int]float64
	mixes  []mix
	songs  []int // dense ids of example songs (indexed test positives)
	reg    landmarkv1.RegistryServiceClient
	fps    sync.Pool
	jobs   chan struct{} // bounds concurrent tracklist jobs
}

func newServer(cfg config) (*server, func(), error) {
	cat, err := catalog.Load(cfg.index)
	if err != nil {
		return nil, nil, err
	}
	if err := catalog.Same(cfg.index, cfg.pairsIndex); err != nil {
		return nil, nil, err
	}
	var closers []func()
	closeAll := func() {
		for _, c := range closers {
			c()
		}
	}
	s := &server{cfg: cfg, cat: cat, titles: map[int]title{}, jobs: make(chan struct{}, 2),
		fps: sync.Pool{New: func() any { return hybrid.NewFingerprinters() }}}
	s.search = &hybrid.Searcher{Cluster: cat.Cluster, Shifts: 4, Extra: []float64{2, 0.5}, Alpha: alpha,
		Timeout: cfg.timeout}
	var c func()
	if s.search.B2, c, err = hybrid.Dial(cfg.coordinator); err != nil {
		return nil, nil, err
	}
	closers = append(closers, c)
	if s.search.Pairs, c, err = hybrid.Dial(cfg.pairsCoordinator); err != nil {
		closeAll()
		return nil, nil, err
	}
	closers = append(closers, c)
	if cfg.registry != "" {
		conn, err := grpc.NewClient(cfg.registry, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			closeAll()
			return nil, nil, fmt.Errorf("dial registry %s: %w", cfg.registry, err)
		}
		closers = append(closers, func() { _ = conn.Close() })
		s.reg = landmarkv1.NewRegistryServiceClient(conn)
	}
	if b, err := os.ReadFile(cfg.titles); err == nil {
		var raw map[string]title
		if err := json.Unmarshal(b, &raw); err != nil {
			closeAll()
			return nil, nil, fmt.Errorf("parse %s: %w", cfg.titles, err)
		}
		for k, v := range raw {
			var id int
			if _, err := fmt.Sscan(k, &id); err == nil {
				s.titles[id] = v
			}
		}
	}
	if s.bpm, err = analysis.ReadBPM(cfg.index); err != nil {
		closeAll()
		return nil, nil, err
	}
	if s.mixes, err = loadMixes(cfg.mixes, strings.Split(cfg.sets, ",")); err != nil {
		slog.Warn("no example mixes", "err", err)
	}
	s.songs = exampleSongs(cfg.manifest, cat, cfg.fmaDir)
	return s, closeAll, nil
}

// The hybrid's calibrated settings (cmd/tracklist's defaults, EVAL): pair
// scores weighted by alpha, a window accepted when its best weighted score
// is at least tAbs and tRatio times the runner-up.
const (
	alpha    = 3
	tAbs     = 10
	tRatio   = 2
	smoothK  = 1
	mergeGap = 2.5
	window   = 10.0
	hop      = 2.5
)

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(web, "web")
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /api/info", s.info)
	mux.HandleFunc("GET /api/examples", s.examples)
	mux.HandleFunc("POST /api/identify", s.identify)
	mux.HandleFunc("POST /api/tracklist", s.tracklist)
	mux.HandleFunc("GET /api/live", s.live)
	return mux
}

// song is how the page shows a track.
type song struct {
	Track  int    `json:"track"`
	Name   string `json:"name"`             // display name
	Artist string `json:"artist,omitempty"` // when known
	Title  string `json:"title,omitempty"`
	Source string `json:"source"` // "FMA 012345" or the reference file
}

func (s *server) song(dense int) song {
	out := song{Track: dense, Name: s.cat.Name(dense), Source: s.cat.Name(dense)}
	if dense < 0 || dense >= len(s.cat.Tracks) {
		return out
	}
	if t, ok := s.titles[s.cat.Tracks[dense].FMAID]; ok && t.Title != "" {
		out.Artist, out.Title = t.Artist, t.Title
		out.Name = t.Title
		if t.Artist != "" {
			out.Name = t.Artist + " - " + t.Title
		}
		return out
	}
	out.Artist, out.Title = prettyName(out.Source)
	out.Name = out.Title
	if out.Artist != "" {
		out.Name = out.Artist + " - " + out.Title
	}
	return out
}

// Reference file names (UnmixDB's) carry label codes and track numbers in
// many styles: "pntg001-04-demir-you_got_it", "[DigitalDiamonds037]_A2_Daana
// _-_Absentia", "04 - Mr.Dee - Evil Angel (Fcode Remix)".
var (
	leadBracket = regexp.MustCompile(`^\[[^\]]*\]\s*`)
	leadCode    = regexp.MustCompile(`^(?:\d{1,2}\.)?[A-Za-z]{2,8}\d{2,4}[A-Za-z]?(?:[\s.-]+|$)`)
	leadTrack   = regexp.MustCompile(`^(?:[A-Za-z]?\d{1,2})(?:\s*-\s*|[\s.]+)`)
	trailCode   = regexp.MustCompile(`\s+-\s+[A-Za-z]+\d{2,4}$`)
)

// prettyName guesses a reference file's artist and title.
func prettyName(raw string) (artist, title string) {
	name := filepath.Base(raw)
	for _, ext := range []string{".mp3", ".wav", ".flac"} {
		name = strings.TrimSuffix(name, ext)
	}
	if i := strings.Index(name, ".excerpt"); i > 0 {
		name = name[:i]
	}
	name = strings.TrimSuffix(name, "_320mp3")
	name = strings.ReplaceAll(name, "_", " ")
	name = leadBracket.ReplaceAllString(name, "")
	name = leadCode.ReplaceAllString(name, "")
	name = leadTrack.ReplaceAllString(name, "")
	name = trailCode.ReplaceAllString(strings.Join(strings.Fields(name), " "), "")
	sep := " - "
	if !strings.Contains(name, sep) {
		sep = "-"
	}
	if a, t, ok := strings.Cut(name, sep); ok && strings.TrimSpace(a) != "" && strings.TrimSpace(t) != "" {
		artist, title = strings.TrimSpace(a), strings.TrimSpace(t)
	} else {
		title = name
	}
	return capitalize(artist), capitalize(title)
}

// capitalize upper-cases the first letter of each word of an all-lower-case
// string and leaves anything else alone.
func capitalize(s string) string {
	if s != strings.ToLower(s) {
		return s
	}
	words := strings.Fields(s)
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

func (s *server) info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"tracks": len(s.cat.Tracks), "live": s.reg != nil,
		"mixes": len(s.mixes), "songs": len(s.songs), "window_s": window, "hop_s": hop})
}

func (s *server) examples(w http.ResponseWriter, _ *http.Request) {
	type mixOut struct {
		Index     int     `json:"index"`
		Name      string  `json:"name"`
		Set       string  `json:"set"`
		Timescale string  `json:"timescale"`
		Effect    string  `json:"effect"`
		Seconds   float64 `json:"seconds"`
		Songs     int     `json:"songs"`
	}
	mixes := make([]mixOut, len(s.mixes))
	for i, m := range s.mixes {
		mixes[i] = mixOut{Index: i, Name: m.Name, Set: m.Set, Timescale: m.Timescale, Effect: m.Effect,
			Seconds: m.AudioS, Songs: len(m.Segments)}
	}
	songs := make([]song, 0, min(len(s.songs), 200))
	for _, d := range s.songs[:min(len(s.songs), 200)] {
		songs = append(songs, s.song(d))
	}
	writeJSON(w, map[string]any{"mixes": mixes, "songs": songs})
}

// mix is one example mix with its ground truth (eval/mixes).
type mix struct {
	File      string    `json:"file"`
	Name      string    `json:"name"`
	Set       string    `json:"set"`
	Timescale string    `json:"timescale"`
	Effect    string    `json:"effect"`
	AudioS    float64   `json:"audio_s"`
	Segments  []segment `json:"segments"`
}

type segment struct {
	Dense    int     `json:"dense"`
	Known    bool    `json:"known"`
	Mode     string  `json:"mode"`
	Pct      float64 `json:"pct"`
	MixStart float64 `json:"mix_start"`
	MixEnd   float64 `json:"mix_end"`
	FadeIn   float64 `json:"fade_in"`
	FadeOut  float64 `json:"fade_out"`
}

func loadMixes(path string, sets []string) ([]mix, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var gt struct {
		Mixes []mix `json:"mixes"`
	}
	if err := json.Unmarshal(b, &gt); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var out []mix
	for _, m := range gt.Mixes {
		if slices.Contains(sets, m.Set) {
			if _, err := os.Stat(m.File); err == nil {
				out = append(out, m)
			}
		}
	}
	return out, nil
}

// exampleSongs returns the dense ids of the manifest's test positives that
// the index holds and whose audio is on disk.
func exampleSongs(manifestPath string, cat *catalog.Catalog, fmaDir string) []int {
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil
	}
	var m struct {
		TestPos []int `json:"test_pos"`
	}
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	want := map[int]bool{}
	for _, id := range m.TestPos {
		want[id] = true
	}
	var out []int
	for _, t := range cat.Tracks {
		if !want[t.FMAID] {
			continue
		}
		if p, ok := cat.Audio(int(t.Dense), fmaDir); ok {
			if _, err := os.Stat(p); err == nil {
				out = append(out, int(t.Dense))
			}
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, format string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf(format, args...)})
}
