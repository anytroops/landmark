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
	return ffmpeg(ctx, path, "-nostdin", "-v", "error",
		"-i", path,
		"-ac", "1", "-ar", strconv.Itoa(sampleRate), "-f", "f32le", "-",
	)
}

// DecodeExcerpt returns seconds of the file at path from start (0: to the
// end) as mono float32 samples at sampleRate, through the ffmpeg audio
// filter graph filter if it is not empty (for example "atempo=1.06", a
// tempo change that keeps the pitch). Seconds count input time. Unlike
// Decode it seeks before decoding, which is fast but not sample-exact, so
// it is for interactive use, not for anything compared with the eval.
func DecodeExcerpt(ctx context.Context, path string, sampleRate int, start, seconds float64, filter string) ([]float32, error) {
	args := []string{"-nostdin", "-v", "error"}
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}
	if seconds > 0 {
		args = append(args, "-t", strconv.FormatFloat(seconds, 'f', 3, 64))
	}
	args = append(args, "-i", path)
	if filter != "" {
		args = append(args, "-af", filter)
	}
	return ffmpeg(ctx, path, append(args, "-ac", "1", "-ar", strconv.Itoa(sampleRate), "-f", "f32le", "-")...)
}

// ffmpeg runs ffmpeg with args, which must write f32le samples to stdout,
// and returns the samples.
func ffmpeg(ctx context.Context, path string, args ...string) ([]float32, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
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
