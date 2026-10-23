package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	landmarkv1 "github.com/anytroops/landmark/gen/landmark/v1"
	"github.com/anytroops/landmark/internal/catalog"
	"github.com/anytroops/landmark/internal/hybrid"
)

func TestPrettyName(t *testing.T) {
	tests := []struct{ raw, artist, title string }{
		{"set044/pntg001-04-demir-you_got_it.excerpt40.mp3", "Demir", "You Got It"},
		{"set044/pntg001-03-ludwig_coenen-l5.excerpt40.mp3", "Ludwig Coenen", "L5"},
		{"set281/[DigitalDiamonds037]_A2_Daana_-_Absentia.excerpt40.mp3", "Daana", "Absentia"},
		{"set281/04 - Mr.Dee - Evil Angel (Syntech Vedeneev Remix).excerpt40.mp3", "Mr.Dee", "Evil Angel (Syntech Vedeneev Remix)"},
		{"set123/04_Hiroshi_Oki_-_Unagui_-_Antiritmo011.excerpt40.mp3", "Hiroshi Oki", "Unagui"},
		{"set230/thn001-02-thomas_jaldemark_-_highway_patrol.excerpt40.mp3", "Thomas Jaldemark", "Highway Patrol"},
		{"set275/Atabey - Vast Plexity.excerpt40.mp3", "Atabey", "Vast Plexity"},
		{"set230/DN020-04_belektron_failgracefully_320mp3.excerpt40.mp3", "", "Belektron Failgracefully"},
		{"set281/02.BrandonPlank-ThatsHot.excerpt40.mp3", "BrandonPlank", "ThatsHot"},
	}
	for _, tt := range tests {
		if a, ti := prettyName(tt.raw); a != tt.artist || ti != tt.title {
			t.Errorf("prettyName(%q) = %q, %q; want %q, %q", tt.raw, a, ti, tt.artist, tt.title)
		}
	}
}

// testServer is a server over a 30-track catalog whose two clusters
// always answer b2Track and pairTrack (-1: nothing): the triplet cluster
// with a fitted tempo of +2% and a strong score, the pair cluster weaker,
// as real answers are. Only the window as is is searched.
func testServer(t *testing.T, b2Track, pairTrack int) *server {
	t.Helper()
	dir := t.TempDir()
	var tracks []catalog.Track
	for i := range 30 {
		tracks = append(tracks, catalog.Track{Dense: uint32(i), FMAID: 1000 + i, Cluster: uint32(i)})
	}
	b, err := json.Marshal(tracks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "catalog.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	answer := func(track int, score uint32, scale float32) hybrid.IdentifyFunc {
		return func(context.Context, *landmarkv1.IdentifyRequest) (*landmarkv1.IdentifyResponse, error) {
			if track < 0 {
				return &landmarkv1.IdentifyResponse{}, nil
			}
			return &landmarkv1.IdentifyResponse{Candidates: []*landmarkv1.Candidate{
				{TrackId: uint32(track), Score: score, Offset: 43, Scale: scale}, {TrackId: 29, Score: 2}}}, nil
		}
	}
	s := &server{cat: cat, titles: map[int]title{1005: {Artist: "Artist", Title: "Five"}}, jobs: make(chan struct{}, 2),
		fps: sync.Pool{New: func() any { return hybrid.NewFingerprinters() }}, cfg: config{workers: 2}}
	s.search = &hybrid.Searcher{B2: answer(b2Track, 400, 1.02), Pairs: answer(pairTrack, 50, 0), Cluster: cat.Cluster,
		Shifts: 4, Alpha: alpha, Timeout: 5 * time.Second}
	return s
}

func post(t *testing.T, s *server, path string, fields map[string]string, file string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if file != "" {
		w, err := mw.CreateFormFile("audio", filepath.Base(file))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	return rec
}

func noise(t *testing.T, seconds int) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	path := filepath.Join(t.TempDir(), "noise.wav")
	if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i",
		fmt.Sprintf("anoisesrc=seed=3:sample_rate=11025:duration=%d", seconds), path).CombinedOutput(); err != nil {
		t.Fatalf("make fixture: %v: %s", err, out)
	}
	return path
}

func TestIdentifyUpload(t *testing.T) {
	s := testServer(t, 5, -1)
	rec := post(t, s, "/api/identify", nil, noise(t, 12))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var res identifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Answer == nil || res.Answer.Track != 5 || res.Answer.Name != "Artist - Five" || !res.Answer.Matched {
		t.Fatalf("answer %+v, want track 5, Artist - Five", res.Answer)
	}
	if !res.Triplets.Matched || res.Pairs.Matched || res.Pairs.Track != -1 {
		t.Errorf("triplets %+v, pairs %+v: want only the triplets to match", res.Triplets, res.Pairs)
	}
	if math.Abs(res.Answer.TempoPct-2) > 1e-4 || math.Abs(res.Answer.OffsetS-43*256.0/11025) > 1e-9 {
		t.Errorf("tempo %v%%, offset %v s; want +2%% (the scale) and 43 frames", res.Answer.TempoPct, res.Answer.OffsetS)
	}
	if res.Seconds != clipSeconds {
		t.Errorf("searched %v s, want %v", res.Seconds, clipSeconds)
	}
	if !strings.HasPrefix(res.Clip, "data:audio/wav;base64,") || len(res.Clip) < 4*clipSeconds*11025*2/3 {
		t.Errorf("clip %.40q... (%d bytes): want the searched 10 s as a WAV data URL", res.Clip, len(res.Clip))
	}
}

func TestIdentifyNeedsAudio(t *testing.T) {
	s := testServer(t, 5, 5)
	if rec := post(t, s, "/api/identify", nil, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("no audio: status %d, want 400", rec.Code)
	}
	if rec := post(t, s, "/api/identify", map[string]string{"example": "random"}, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("no example songs: status %d, want 400", rec.Code)
	}
}

// TestTracklistStreams: a mix the clusters answer with one song throughout
// streams every window, that song's start and end, the analysis, and done,
// in that order.
func TestTracklistStreams(t *testing.T) {
	s := testServer(t, 7, 7)
	rec := post(t, s, "/api/tracklist", nil, noise(t, 40))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var kinds []string
	windows := 0
	var ends []entryOut
	for _, line := range strings.Split(strings.TrimSpace(rec.Body.String()), "\n") {
		var m struct {
			Type  string   `json:"type"`
			Kind  string   `json:"kind"`
			Entry entryOut `json:"entry"`
		}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		switch m.Type {
		case "window":
			windows++
		case "song":
			kinds = append(kinds, m.Kind)
			if m.Kind == "end" {
				ends = append(ends, m.Entry)
			}
		default:
			kinds = append(kinds, m.Type)
		}
	}
	if windows != 13 { // 40 s: windows every 27,520 samples, 0 to 29.954 s (D48)
		t.Errorf("%d windows, want 13", windows)
	}
	if want := []string{"start", "start", "end", "analyzing", "analysis", "done"}; !slices.Equal(kinds, want) {
		t.Errorf("messages %v, want %v", kinds, want)
	}
	// The last window starts at 12 x 27,520 samples (29.954 s).
	if len(ends) != 1 || ends[0].Track != 7 || ends[0].StartS != 0 || math.Abs(ends[0].EndS-39.954) > 1e-9 ||
		math.Abs(ends[0].TempoPct-2) > 1e-4 {
		t.Errorf("entries %+v, want track 7 from 0 to 39.954 s at +2%%", ends)
	}
}

func TestTempoFilter(t *testing.T) {
	tests := []struct {
		pct    float64
		kind   string
		filter string
		in     float64
	}{
		{0, "turntable", "", 10},
		{6, "keylock", "atempo=1.0600", 10.6},
		{-4, "turntable", "aresample=11025,asetrate=10584.00,aresample=11025", 9.6},
	}
	for _, tt := range tests {
		f, in := tempoFilter(tt.pct, tt.kind)
		if f != tt.filter || math.Abs(in-tt.in) > 1e-9 {
			t.Errorf("tempoFilter(%v, %s) = %q, %v; want %q, %v", tt.pct, tt.kind, f, in, tt.filter, tt.in)
		}
	}
}

func TestLiveWithoutRegistry(t *testing.T) {
	s := testServer(t, -1, -1)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/live", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}

func TestWav16(t *testing.T) {
	b := wav16([]float32{0, 0.5, -1, 2}, 11025)
	if len(b) != 44+8 || string(b[:4]) != "RIFF" || string(b[8:16]) != "WAVEfmt " || string(b[36:40]) != "data" {
		t.Fatalf("bad header: % x", b[:44])
	}
	want := []int16{0, 16384, -32767, 32767} // clipped to [-1, 1]
	for i, w := range want {
		if got := int16(uint16(b[44+2*i]) | uint16(b[45+2*i])<<8); got != w {
			t.Errorf("sample %d = %d, want %d", i, got, w)
		}
	}
}

func TestAudioRoutes(t *testing.T) {
	s := testServer(t, -1, -1)
	wav := noise(t, 1)
	s.mixes = []mix{{File: wav, Name: "m"}}
	tests := []struct {
		path string
		code int
	}{
		{"/api/mixes/0/audio", http.StatusOK},
		{"/api/mixes/1/audio", http.StatusNotFound},
		{"/api/mixes/x/audio", http.StatusNotFound},
		{"/api/songs/3/audio", http.StatusNotFound}, // FMA track, no -fma-dir
		{"/api/songs/99/audio", http.StatusNotFound},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tt.path, nil)
		req.Header.Set("Range", "bytes=0-99")
		s.routes().ServeHTTP(rec, req)
		got := rec.Code
		if got == http.StatusPartialContent { // the range was served
			got = http.StatusOK
		}
		if got != tt.code {
			t.Errorf("%s: status %d, want %d", tt.path, rec.Code, tt.code)
		}
	}
}
