package main

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/monitor"
)

// TestListenersHearTheOfflineSamples: a listener joining mid-broadcast
// decodes exactly audio.Decode's samples from the position the start header
// names.
func TestListenersHearTheOfflineSamples(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	wav := filepath.Join(dir, "a.wav")
	if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i",
		"anoisesrc=seed=5:sample_rate=22050:duration=6", "-ac", "2", wav).CombinedOutput(); err != nil {
		t.Fatalf("make fixture: %v: %s", err, out)
	}
	want, err := audio.Decode(context.Background(), wav, audio.SampleRate)
	if err != nil {
		t.Fatal(err)
	}
	var sched bytes.Buffer
	s := newServer(float64(time.Now().UnixNano())/1e9, &lockedWriter{w: &sched})
	st := s.add("st01")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = st.broadcast(ctx, []string{wav}, false) }()
	ts := httptest.NewServer(s.handler())
	defer ts.Close()

	time.Sleep(time.Second) // join mid-broadcast
	resp, err := http.Get(ts.URL + "/stream/st01")
	if err != nil {
		t.Fatal(err)
	}
	first, err := strconv.ParseFloat(resp.Header.Get(monitor.StartHeader), 64)
	if err != nil {
		t.Fatal(err)
	}
	off := int(math.Round((first - s.start) * audio.SampleRate))
	if off < audio.SampleRate/2 || off > 3*audio.SampleRate {
		t.Fatalf("joined at sample %d, want about 1 s in", off)
	}
	stream, err := audio.NewStream(context.Background(), resp.Body, audio.SampleRate)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	got := make([]float32, 0, 2*audio.SampleRate)
	buf := make([]float32, 1000)
	for len(got) < 2*audio.SampleRate {
		n, err := stream.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, buf[:n]...)
	}
	got = got[:2*audio.SampleRate]
	if !slices.Equal(got, want[off:off+len(got)]) {
		t.Fatal("streamed samples differ from the offline decode")
	}

	var line struct {
		Station   string  `json:"station"`
		File      string  `json:"file"`
		StartUnix float64 `json:"start_unix"`
		EndUnix   float64 `json:"end_unix"`
	}
	if err := json.Unmarshal(bytes.SplitN(sched.Bytes(), []byte("\n"), 2)[0], &line); err != nil {
		t.Fatalf("schedule %q: %v", sched.String(), err)
	}
	if line.Station != "st01" || line.File != wav || line.StartUnix != s.start ||
		math.Abs(line.EndUnix-line.StartUnix-float64(len(want))/audio.SampleRate) > 1e-9 {
		t.Errorf("schedule line %+v", line)
	}
}

func TestUnknownStation(t *testing.T) {
	ts := httptest.NewServer(newServer(0, &lockedWriter{w: &bytes.Buffer{}}).handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/stream/nope")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d", resp.StatusCode)
	}
}
