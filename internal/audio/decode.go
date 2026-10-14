// Package audio decodes audio files to mono float32 PCM through an ffmpeg pipe.
// Nothing is cached to disk.
package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// SampleRate is the rate every fingerprint is computed at.
const SampleRate = 11025

// ErrDecode marks input ffmpeg could not decode (corrupt, truncated, or empty).
// Callers check it with errors.Is to skip and count bad files instead of failing.
var ErrDecode = errors.New("audio: decode failed")

// Decode returns the file at path as mono float32 samples at sampleRate.
// The ffmpeg process is killed if ctx is cancelled.
func Decode(ctx context.Context, path string, sampleRate int) ([]float32, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-nostdin", "-v", "error",
		"-i", path,
		"-ac", "1", "-ar", strconv.Itoa(sampleRate), "-f", "f32le", "-",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("audio: decode %s: %w", path, ctx.Err())
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("%w: %s: %s", ErrDecode, path, strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("audio: run ffmpeg: %w", err)
	}
	pcm, err := parseF32LE(stdout.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrDecode, path, err)
	}
	if len(pcm) == 0 {
		return nil, fmt.Errorf("%w: %s: decoded to zero samples", ErrDecode, path)
	}
	return pcm, nil
}

// parseF32LE converts raw little-endian float32 bytes to samples.
func parseF32LE(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("pcm length %d is not a multiple of 4", len(b))
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out, nil
}
