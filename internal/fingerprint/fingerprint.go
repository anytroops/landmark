// Package fingerprint computes landmark fingerprints: log-magnitude STFT,
// constellation peaks, and peak-pair hashes.
//
// It is a port of prototype/fingerprint.py and must match it exactly up to
// FFT rounding (D17): same framing, window, max filter, tie order, ranking,
// and pairing. Parity is tested against testdata/golden.
//
// Pipeline (D9, D10):
//
//	log-magnitude STFT -> 2D local maxima above an absolute floor
//	-> keep the strongest N peaks per time block
//	-> pair each anchor with its next Fanout peaks in a frequency-bounded zone
//	-> hash = f1<<15 | f2<<6 | dt, posted at anchor frame t1.
package fingerprint

import (
	"math"
	"math/cmplx"
	"sort"

	"gonum.org/v1/gonum/dsp/fourier"
)

const (
	fBits  = 9
	dtBits = 6
	// NBins is the number of FFT bins used (of NFFT/2 + 1).
	NBins = 1 << fBits
	// MaxDT is the largest dt a hash can hold, in frames.
	MaxDT = 1<<dtBits - 1
)

// Params mirrors prototype.fingerprint.Params. JSON tags match the Python
// field names so golden files can be checked against DefaultParams.
type Params struct {
	NFFT          int     `json:"n_fft"`
	Hop           int     `json:"hop"`
	NbhdF         int     `json:"nbhd_f"`
	NbhdT         int     `json:"nbhd_t"`
	FloorDB       float64 `json:"floor_db"`
	BlockFrames   int     `json:"block_frames"`
	PeaksPerBlock int     `json:"peaks_per_block"`
	Fanout        int     `json:"fanout"`
	MinDT         int     `json:"min_dt"`
	MaxDT         int     `json:"max_dt"`
	MaxDF         int     `json:"max_df"`
}

// DefaultParams matches prototype.fingerprint.DEFAULT. A function, not a
// package variable, so no caller can mutate shared state.
func DefaultParams() Params {
	return Params{
		NFFT: 1024, Hop: 256, NbhdF: 17, NbhdT: 17, FloorDB: -20,
		BlockFrames: 43, PeaksPerBlock: 30, Fanout: 5, MinDT: 1, MaxDT: MaxDT, MaxDF: 64,
	}
}

// Landmark is one hashed peak pair, posted at its anchor frame.
type Landmark struct {
	Hash uint32
	T    uint32
}

// Peak is a constellation point.
type Peak struct {
	T, F int
	Mag  float64
}

// Spectrogram is a row-major Frames x Bins matrix of dB values. The pair
// fingerprint's has NBins linear bins; other front ends (D33) set their own.
type Spectrogram struct {
	Frames int
	Bins   int
	Data   []float64
}

// At returns the value at frame t, bin f.
func (s Spectrogram) At(t, f int) float64 { return s.Data[t*s.Bins+f] }

// Fingerprinter holds FFT plans and scratch buffers. It is not safe for
// concurrent use: give each goroutine its own.
type Fingerprinter struct {
	p      Params
	fft    *fourier.FFT
	window []float64
	frame  []float64
	coeffs []complex128
}

// New returns a Fingerprinter for p.
func New(p Params) *Fingerprinter {
	w := make([]float64, p.NFFT)
	for n := range w {
		// numpy.hanning: symmetric, 0.5 - 0.5 cos(2 pi n / (M - 1)).
		w[n] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(n)/float64(p.NFFT-1))
	}
	return &Fingerprinter{
		p:      p,
		fft:    fourier.NewFFT(p.NFFT),
		window: w,
		frame:  make([]float64, p.NFFT),
		coeffs: make([]complex128, p.NFFT/2+1),
	}
}

// Spectrogram computes the log-magnitude STFT. Frame k starts at sample k*Hop;
// there is no centering or padding.
func (fp *Fingerprinter) Spectrogram(pcm []float32) Spectrogram {
	p := fp.p
	if len(pcm) < p.NFFT {
		return Spectrogram{}
	}
	frames := (len(pcm)-p.NFFT)/p.Hop + 1
	data := make([]float64, frames*NBins)
	for t := 0; t < frames; t++ {
		start := t * p.Hop
		for n := range fp.frame {
			fp.frame[n] = float64(pcm[start+n]) * fp.window[n]
		}
		fp.coeffs = fp.fft.Coefficients(fp.coeffs, fp.frame)
		row := data[t*NBins : (t+1)*NBins]
		for f := range row {
			row[f] = 20 * math.Log10(cmplx.Abs(fp.coeffs[f])+1e-10)
		}
	}
	return Spectrogram{Frames: frames, Bins: NBins, Data: data}
}

// FindPeaks returns local maxima above FloorDB, keeping the strongest
// PeaksPerBlock per block of BlockFrames frames, sorted by (t, f).
func FindPeaks(s Spectrogram, p Params) []Peak {
	if s.Frames == 0 {
		return nil
	}
	local := maxFilter(s, p.NbhdT, p.NbhdF)
	var peaks []Peak // row-major (t, f) order, like numpy.nonzero
	for t := 0; t < s.Frames; t++ {
		for f := 0; f < s.Bins; f++ {
			v := s.At(t, f)
			if v == local[t*s.Bins+f] && v > p.FloorDB {
				peaks = append(peaks, Peak{T: t, F: f, Mag: v})
			}
		}
	}

	// Rank within each block by descending magnitude. Stable, so equal
	// magnitudes keep (t, f) order, matching numpy.lexsort.
	sort.SliceStable(peaks, func(i, j int) bool {
		bi, bj := peaks[i].T/p.BlockFrames, peaks[j].T/p.BlockFrames
		if bi != bj {
			return bi < bj
		}
		return peaks[i].Mag > peaks[j].Mag
	})
	kept := peaks[:0]
	block, rank := -1, 0
	for _, pk := range peaks {
		if b := pk.T / p.BlockFrames; b != block {
			block, rank = b, 0
		}
		if rank < p.PeaksPerBlock {
			kept = append(kept, pk)
		}
		rank++
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].T != kept[j].T {
			return kept[i].T < kept[j].T
		}
		return kept[i].F < kept[j].F
	})
	return kept
}

// maxFilter is scipy.ndimage.maximum_filter(size=(sizeT, sizeF),
// mode="constant", cval=-inf). Max over a rectangle is separable, so filter
// along f, then along t, each with maxFilter1D. Sizes must be odd.
func maxFilter(s Spectrogram, sizeT, sizeF int) []float64 {
	tmp := make([]float64, len(s.Data))
	out := make([]float64, len(s.Data))
	sc := newMaxScratch(max(s.Frames, s.Bins), max(sizeT, sizeF))
	for t := 0; t < s.Frames; t++ { // along f: contiguous rows
		maxFilter1D(s.Data, tmp, t*s.Bins, 1, s.Bins, sizeF/2, sc)
	}
	for f := 0; f < s.Bins; f++ { // along t: stride Bins
		maxFilter1D(tmp, out, f, s.Bins, s.Frames, sizeT/2, sc)
	}
	return out
}

type maxScratch struct{ pad, g, r []float64 }

func newMaxScratch(n, w int) *maxScratch {
	m := n + 2*w
	return &maxScratch{make([]float64, m), make([]float64, m), make([]float64, m)}
}

// maxFilter1D writes the max over [i-h, i+h] (out of range = -inf) for the n
// elements src[off], src[off+stride], ... into dst at the same positions.
//
// van Herk/Gil-Werman: split the -inf-padded sequence into blocks of width
// w = 2h+1, take running maxes forward (g) and backward (r) within each
// block. Any width-w window spans at most two blocks, so its max is
// max(r[start], g[end]): about 3 comparisons per element for any w, versus w
// for the direct loop. Plain > comparisons: values are finite (dB of
// |X| + 1e-10), so math.Max's NaN and signed-zero handling is unneeded.
func maxFilter1D(src, dst []float64, off, stride, n, h int, sc *maxScratch) {
	w := 2*h + 1
	m := n + 2*h
	m += (w - m%w) % w // round up to whole blocks
	pad, g, r := sc.pad[:m], sc.g[:m], sc.r[:m]
	negInf := math.Inf(-1)
	for i := range pad {
		pad[i] = negInf
	}
	for i := 0; i < n; i++ {
		pad[h+i] = src[off+i*stride]
	}
	for b := 0; b < m; b += w {
		g[b] = pad[b]
		for i := b + 1; i < b+w; i++ {
			g[i] = g[i-1]
			if pad[i] > g[i] {
				g[i] = pad[i]
			}
		}
		r[b+w-1] = pad[b+w-1]
		for i := b + w - 2; i >= b; i-- {
			r[i] = r[i+1]
			if pad[i] > r[i] {
				r[i] = pad[i]
			}
		}
	}
	for i := 0; i < n; i++ {
		v := r[i] // window over pad[i .. i+w-1] is centered on element i
		if g[i+w-1] > v {
			v = g[i+w-1]
		}
		dst[off+i*stride] = v
	}
}

// Pair hashes each anchor with its next Fanout peaks satisfying
// MinDT <= dt <= MaxDT and |df| <= MaxDF. Peaks must be sorted by (t, f).
func Pair(peaks []Peak, p Params) []Landmark {
	var out []Landmark
	for i, a := range peaks {
		n := 0
		for j := i + 1; j < len(peaks) && n < p.Fanout; j++ {
			b := peaks[j]
			dt := b.T - a.T
			if dt > p.MaxDT {
				break // sorted by t: every later peak is further away
			}
			if dt < p.MinDT || abs(b.F-a.F) > p.MaxDF {
				continue
			}
			h := uint32(a.F)<<(fBits+dtBits) | uint32(b.F)<<dtBits | uint32(dt)
			out = append(out, Landmark{Hash: h, T: uint32(a.T)})
			n++
		}
	}
	return out
}

// Fingerprint returns the landmarks of pcm, in anchor order.
func (fp *Fingerprinter) Fingerprint(pcm []float32) []Landmark {
	return Pair(FindPeaks(fp.Spectrogram(pcm), fp.p), fp.p)
}

// FingerprintQuery fingerprints pcm on nShifts sub-hop grids, Hop/nShifts
// samples apart, and returns the union sorted by (Hash, T) with exact
// duplicates removed (D15). Used on the query path only.
func (fp *Fingerprinter) FingerprintQuery(pcm []float32, nShifts int) []Landmark {
	step := fp.p.Hop / nShifts
	var all []Landmark
	for k := 0; k < nShifts; k++ {
		if k*step >= len(pcm) {
			break
		}
		all = append(all, fp.Fingerprint(pcm[k*step:])...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Hash != all[j].Hash {
			return all[i].Hash < all[j].Hash
		}
		return all[i].T < all[j].T
	})
	out := all[:0]
	for i, l := range all {
		if i == 0 || l != all[i-1] {
			out = append(out, l)
		}
	}
	return out
}

// Unpack splits a hash into (f1, f2, dt).
func Unpack(h uint32) (f1, f2, dt int) {
	return int(h >> (fBits + dtBits)), int(h>>dtBits) & (NBins - 1), int(h) & MaxDT
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
