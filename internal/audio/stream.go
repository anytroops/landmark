package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Stream decodes audio to mono float32 PCM as it arrives, for live sources:
// an HTTP body, or a file read at a paced rate. Read returns samples as
// ffmpeg produces them.
type Stream struct {
	cmd    *exec.Cmd
	in     io.Closer // the piped input, if any
	out    io.ReadCloser
	stderr *syncBuffer
	raw    []byte // bytes read from ffmpeg, not yet returned as samples
	done   bool
	waited bool
	err    error
}

// NewStream decodes r, in any format ffmpeg can read from a pipe (WAV, MP3,
// AAC, ...), at sampleRate. Closing the Stream stops ffmpeg and closes r;
// cancelling ctx stops ffmpeg. Over a pipe ffmpeg cannot use an MP3's
// gapless header, so MP3 input keeps its encoder delay (about 20 ms) that
// Decode trims; WAV input decodes exactly as Decode does.
func NewStream(ctx context.Context, r io.ReadCloser, sampleRate int) (*Stream, error) {
	return startStream(ctx, r, sampleRate, "-probesize", "65536", "-i", "pipe:0")
}

// NewFileStream decodes the file at path, read at readRate times real time
// (0: as fast as possible), for simulated live sources.
func NewFileStream(ctx context.Context, path string, sampleRate int, readRate float64) (*Stream, error) {
	var args []string
	if readRate > 0 {
		args = append(args, "-readrate", strconv.FormatFloat(readRate, 'f', -1, 64))
	}
	return startStream(ctx, nil, sampleRate, append(args, "-i", path)...)
}

func startStream(ctx context.Context, stdin io.ReadCloser, sampleRate int, input ...string) (*Stream, error) {
	args := []string{"-v", "error"}
	if stdin == nil {
		args = append(args, "-nostdin")
	}
	args = append(append(args, input...), "-ac", "1", "-ar", strconv.Itoa(sampleRate), "-flush_packets", "1", "-f", "f32le", "pipe:1")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	s := &Stream{cmd: cmd, stderr: &syncBuffer{}}
	cmd.Stderr = s.stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("audio: stream pipe: %w", err)
	}
	// Copy the input ourselves: with cmd.Stdin set to a plain reader, Wait
	// would wait for a copy that blocks as long as a live source stays open.
	var pw io.WriteCloser
	if stdin != nil {
		if pw, err = cmd.StdinPipe(); err != nil {
			return nil, fmt.Errorf("audio: stream pipe: %w", err)
		}
		s.in = stdin
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("audio: run ffmpeg: %w", err)
	}
	if pw != nil {
		go func() {
			_, _ = io.Copy(pw, stdin) // ends when either side closes
			_ = pw.Close()
		}()
	}
	s.out = out
	return s, nil
}

// Read fills dst with the next samples, blocking until at least one is
// available. At the end of the input it returns io.EOF; a decode failure
// is an ErrDecode.
func (s *Stream) Read(dst []float32) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	for len(s.raw) < 4 {
		if s.done {
			return 0, s.finish()
		}
		buf := make([]byte, 4*len(dst)-len(s.raw))
		n, err := s.out.Read(buf)
		s.raw = append(s.raw, buf[:n]...)
		if err != nil {
			s.done = true
			if !errors.Is(err, io.EOF) {
				s.err = fmt.Errorf("audio: read ffmpeg output: %w", err)
			}
		}
	}
	n := min(len(dst), len(s.raw)/4)
	for i := range n {
		dst[i] = math.Float32frombits(binary.LittleEndian.Uint32(s.raw[4*i:]))
	}
	s.raw = s.raw[4*n:]
	return n, nil
}

// finish waits for ffmpeg once the output has ended and reports how.
func (s *Stream) finish() error {
	if !s.waited {
		s.waited = true
		if err := s.cmd.Wait(); err != nil && s.err == nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				s.err = fmt.Errorf("%w: stream: %s", ErrDecode, strings.TrimSpace(s.stderr.String()))
			} else {
				s.err = fmt.Errorf("audio: ffmpeg: %w", err)
			}
		}
		if s.err == nil && len(s.raw) != 0 {
			s.err = fmt.Errorf("%w: stream ended inside a sample", ErrDecode)
		}
	}
	if s.err != nil {
		return s.err
	}
	return io.EOF
}

// Close stops ffmpeg and closes the piped input.
func (s *Stream) Close() error {
	if !s.waited {
		s.waited = true
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	}
	if s.in != nil {
		return s.in.Close()
	}
	return nil
}

// syncBuffer is a bytes.Buffer safe for ffmpeg's stderr copier and readers.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
