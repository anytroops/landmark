package triplet

import (
	"fmt"

	"github.com/anytroops/landmark/internal/fingerprint"
)

// Stream fingerprints windows of a signal that arrives in pieces, computing
// each spectrogram frame once (D48). A query fingerprint is computed on
// nShifts grids Hop/nShifts samples apart (D15); when every window starts on
// a multiple of that step, all windows' grids are subsets of one grid of the
// stream, so Stream computes that grid's frames on both filterbanks (D35) as
// samples arrive and cuts each window's spectrograms out of them. Peak
// picking and hashing stay per window. Query(start, n) equals
// FingerprintQuery(signal[start:start+n], nShifts) landmark for landmark:
// each frame is the same arithmetic on the same samples.
//
// A Stream is not safe for concurrent use.
type Stream struct {
	p      Params // the fingerprint's
	shifts int
	step   int            // the grid: p.Hop / shifts samples
	grid   *Fingerprinter // p with Hop = step
	half   []band

	buf      []float32 // samples from bufStart on, those later frames still need
	bufStart int
	rows     [2][]float64 // frames rowStart.. on each filterbank, p.NBands values each
	rowStart int
	frames   int // frames computed: frame k covers samples [k*step, k*step+NFFT)
}

// NewStream returns a Stream for p's query fingerprint on nShifts grids.
func NewStream(p Params, nShifts int) (*Stream, error) {
	if nShifts < 1 || p.Hop%nShifts != 0 {
		return nil, fmt.Errorf("triplet: hop %d does not split into %d shifts", p.Hop, nShifts)
	}
	g := p
	g.Hop = p.Hop / nShifts
	return &Stream{p: p, shifts: nShifts, step: g.Hop, grid: New(g), half: sparse(Filterbank(HalfBand(p)))}, nil
}

// Step is the grid: windows must start on a multiple of it.
func (s *Stream) Step() int { return s.step }

// Push appends samples and computes every frame they complete.
func (s *Stream) Push(x []float32) {
	s.buf = append(s.buf, x...)
	from := s.frames*s.step - s.bufStart
	if len(s.buf)-from < s.p.NFFT {
		return
	}
	specs := s.grid.spectrograms(s.buf[from:], s.grid.bands, s.half)
	for i := range s.rows {
		s.rows[i] = append(s.rows[i], specs[i].Data...)
	}
	s.frames += specs[0].Frames
	// Samples before the next frame's start are done with.
	if drop := s.frames*s.step - s.bufStart; drop > 0 {
		s.buf = append(s.buf[:0], s.buf[drop:]...)
		s.bufStart += drop
	}
}

// Ready reports whether every frame Query(start, n) needs is computed.
func (s *Stream) Ready(start, n int) bool {
	last := -1
	for k := range s.shifts {
		if c := s.count(n, k); c > 0 {
			last = max(last, start/s.step+k+(c-1)*s.shifts)
		}
	}
	return last < s.frames
}

// count is how many frames shift k of an n-sample window has (as
// Spectrogram computes them).
func (s *Stream) count(n, k int) int {
	if n-k*s.step < s.p.NFFT {
		return 0
	}
	return (n-k*s.step-s.p.NFFT)/s.p.Hop + 1
}

// Query returns the query fingerprint of samples [start, start+n): start a
// multiple of Step, not yet forgotten, and Ready.
func (s *Stream) Query(start, n int) ([]Landmark, error) {
	if start%s.step != 0 || start < s.rowStart*s.step {
		return nil, fmt.Errorf("triplet: window at sample %d is off the %d-sample grid or forgotten", start, s.step)
	}
	if !s.Ready(start, n) {
		return nil, fmt.Errorf("triplet: window at sample %d is not complete", start)
	}
	bins := s.p.NBands
	var all []Landmark
	for k := range s.shifts {
		if k*s.step >= n {
			break
		}
		c := s.count(n, k)
		first := start/s.step + k - s.rowStart
		for i := range s.rows {
			spec := fingerprint.Spectrogram{Bins: bins}
			if c > 0 {
				spec.Frames, spec.Data = c, make([]float64, c*bins)
				for t := range c {
					src := (first + t*s.shifts) * bins
					copy(spec.Data[t*bins:(t+1)*bins], s.rows[i][src:src+bins])
				}
			}
			all = append(all, Triplets(fingerprint.FindPeaks(spec, s.p.PeakParams()), s.p)...)
		}
	}
	return union(all), nil
}

// Forget drops the frames of samples before sample, which no later window
// may need.
func (s *Stream) Forget(sample int) {
	k := min(sample/s.step, s.frames) - s.rowStart
	if k <= 0 {
		return
	}
	for i := range s.rows {
		s.rows[i] = append(s.rows[i][:0], s.rows[i][k*s.p.NBands:]...)
	}
	s.rowStart += k
}
