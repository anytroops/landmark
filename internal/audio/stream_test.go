package audio

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// readAll reads a stream in uneven chunks.
func readAll(t *testing.T, s *Stream) ([]float32, error) {
	t.Helper()
	var out []float32
	buf := make([]float32, 777)
	for {
		n, err := s.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			return out, err
		}
	}
}

// writeWAV writes 1 s of a sine as 32-bit float WAV at 22,050 Hz.
func writeWAV(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "sine.wav")
	out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=22050:duration=1",
		"-c:a", "pcm_f32le", path).CombinedOutput()
	if err != nil {
		t.Fatalf("make fixture: %v: %s", err, out)
	}
	return path
}

func TestStreamMatchesDecode(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	sine, wav := writeSine(t, dir), writeWAV(t, dir)
	decode := func(path string) []float32 {
		pcm, err := Decode(context.Background(), path, SampleRate)
		if err != nil {
			t.Fatal(err)
		}
		return pcm
	}
	tests := []struct {
		name string
		open func() (*Stream, error)
		want []float32
	}{
		{"wav pipe", func() (*Stream, error) {
			f, err := os.Open(wav)
			if err != nil {
				return nil, err
			}
			return NewStream(context.Background(), f, SampleRate)
		}, decode(wav)},
		{"mp3 file", func() (*Stream, error) { return NewFileStream(context.Background(), sine, SampleRate, 0) }, decode(sine)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := tt.open()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			got, err := readAll(t, s)
			if !errors.Is(err, io.EOF) {
				t.Fatalf("end of stream: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("%d samples, Decode %d, or values differ", len(got), len(tt.want))
			}
		})
	}
}

func TestFileStreamIsPaced(t *testing.T) {
	requireFFmpeg(t)
	sine := writeSine(t, t.TempDir()) // 1 s
	s, err := NewFileStream(context.Background(), sine, SampleRate, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	start := time.Now()
	if _, err := readAll(t, s); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	// ffmpeg sends the first 0.5 s at once (-readrate_initial_burst), then
	// paces the rest: at least 0.5 s / 4 = 125 ms. Unpaced, it takes ~20 ms.
	if d := time.Since(start); d < 100*time.Millisecond {
		t.Errorf("1 s at 4x read in %v, want at least 125 ms", d)
	}
}

func TestStreamReportsBadInput(t *testing.T) {
	requireFFmpeg(t)
	s, err := NewStream(context.Background(), io.NopCloser(strings.NewReader("definitely not audio")), SampleRate)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := readAll(t, s); !errors.Is(err, ErrDecode) {
		t.Errorf("garbage: %v, want ErrDecode", err)
	}
}

func TestStreamCloseStopsFFmpeg(t *testing.T) {
	requireFFmpeg(t)
	r, w := io.Pipe() // a source that never ends
	defer func() { _ = w.Close() }()
	s, err := NewStream(context.Background(), r, SampleRate)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = s.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop ffmpeg")
	}
}
