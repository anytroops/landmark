package audio

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
}

// writeSine writes a 1 s, 440 Hz MP3 into dir and returns its path.
func writeSine(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "sine.mp3")
	out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100:duration=1",
		"-b:a", "128k", path).CombinedOutput()
	if err != nil {
		t.Fatalf("make fixture: %v: %s", err, out)
	}
	return path
}

func TestDecode(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	sine := writeSine(t, dir)
	garbage := filepath.Join(dir, "garbage.mp3")
	if err := os.WriteFile(garbage, []byte("definitely not audio"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		path        string
		wantDecode  bool // want an ErrDecode
		wantSamples int  // approximate; ignored on error
	}{
		{name: "valid mp3", path: sine, wantSamples: SampleRate},
		{name: "garbage bytes", path: garbage, wantDecode: true},
		{name: "missing file", path: filepath.Join(dir, "nope.mp3"), wantDecode: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pcm, err := Decode(context.Background(), tc.path, SampleRate)
			if tc.wantDecode {
				if !errors.Is(err, ErrDecode) {
					t.Fatalf("err = %v, want ErrDecode", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			// MP3 adds encoder delay and frame padding, so allow 10%.
			if diff := math.Abs(float64(len(pcm) - tc.wantSamples)); diff > 0.1*float64(tc.wantSamples) {
				t.Errorf("got %d samples, want about %d", len(pcm), tc.wantSamples)
			}
		})
	}
}

func TestDecodeExcerpt(t *testing.T) {
	requireFFmpeg(t)
	sine := writeSine(t, t.TempDir())
	tests := []struct {
		name           string
		start, seconds float64
		filter         string
		want           float64 // seconds of output
	}{
		{"whole file", 0, 0, "", 1},
		{"excerpt", 0.25, 0.5, "", 0.5},
		{"keylock twice as fast", 0, 1, "atempo=2", 0.5},
		{"turntable twice as fast", 0, 1, "asetrate=88200,aresample=44100", 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pcm, err := DecodeExcerpt(context.Background(), sine, SampleRate, tt.start, tt.seconds, tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			if got := float64(len(pcm)) / SampleRate; math.Abs(got-tt.want) > 0.06 {
				t.Errorf("%.3f s of audio, want about %.3f", got, tt.want)
			}
		})
	}
	if _, err := DecodeExcerpt(context.Background(), sine, SampleRate, 0, 0, "nosuchfilter"); !errors.Is(err, ErrDecode) {
		t.Errorf("bad filter: %v, want ErrDecode", err)
	}
}

func TestDecodeHonorsCancellation(t *testing.T) {
	requireFFmpeg(t)
	sine := writeSine(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	_, err := Decode(ctx, sine, SampleRate)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestParseF32LE(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		want    []float32
		wantErr bool
	}{
		{name: "empty", in: nil, want: []float32{}},
		{name: "one and minus half", in: []byte{0, 0, 0x80, 0x3f, 0, 0, 0, 0xbf}, want: []float32{1, -0.5}},
		{name: "truncated", in: []byte{0, 0, 0x80}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseF32LE(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}
