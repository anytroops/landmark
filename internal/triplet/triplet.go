// Package triplet computes the tempo-invariant triplet fingerprint (D31
// option B2, D33): a log-frequency spectrogram, constellation peaks, and
// hashes of three peaks built from values a tempo change cannot move.
//
// It is a port of prototype/b2.py and must match it up to FFT rounding (D17):
// same framing, window, filterbank, peak rule (fingerprint.FindPeaks), target
// order, hash layout, and output order. Parity is tested against
// testdata/golden_b2.
//
// Hash layout (24 bits):
//
//	band << 20 | rt << 14 | (f_j - f_i + Zone) << 7 | (f_k - f_i + Zone)
//
// where band is the anchor's band / BandWidth, rt is floor(RTLevels *
// (t_j - t_i) / (t_k - t_i)), and f is the log-frequency band index. Each
// landmark also carries span = t_k - t_i and the anchor band, which the
// matcher uses to estimate each match's tempo factor.
package triplet

import (
	"math"
	"math/cmplx"
	"sort"

	"gonum.org/v1/gonum/dsp/fourier"

	"github.com/anytroops/landmark/internal/audio"
	"github.com/anytroops/landmark/internal/fingerprint"
)

// Params mirrors prototype.b2.Params; JSON tags match the Python names.
type Params struct {
	NFFT           int     `json:"n_fft"`
	Hop            int     `json:"hop"`
	BandsPerOctave int     `json:"bands_per_octave"`
	FMin           float64 `json:"fmin"`
	NBands         int     `json:"n_bands"`
	NbhdT          int     `json:"nbhd_t"`
	NbhdF          int     `json:"nbhd_f"`
	FloorDB        float64 `json:"floor_db"`
	BlockFrames    int     `json:"block_frames"`
	PeaksPerBlock  int     `json:"peaks_per_block"`
	Fanout         int     `json:"fanout"`
	MaxDT          int     `json:"max_dt"`
	Zone           int     `json:"zone"`
	BandWidth      int     `json:"band_width"`
	RTLevels       int     `json:"rt_levels"`
}

// DefaultParams matches prototype.b2.DEFAULT.
func DefaultParams() Params {
	return Params{
		NFFT: 4096, Hop: 256, BandsPerOctave: 36, FMin: 55, NBands: 234,
		NbhdT: 17, NbhdF: 9, FloorDB: -8, BlockFrames: 43, PeaksPerBlock: 30,
		Fanout: 5, MaxDT: 63, Zone: 36, BandWidth: 18, RTLevels: 64,
	}
}

// Landmark is one triplet, posted at its anchor frame T.
type Landmark struct {
	Hash uint32
	T    uint32
	Span uint8 // t_k - t_i, frames
	Band uint8 // anchor band
}

// band is one row of the filterbank: weights for FFT bins Lo .. Lo+len(W)-1.
type band struct {
	Lo int
	W  []float64
}

// Filterbank returns prototype.b2.filterbank as sparse rows: triangular
// filters centered every 1/BandsPerOctave octave from FMin, each normalized to
// sum 1; a band too narrow to hold any FFT bin uses the nearest one.
func Filterbank(p Params) [][]float64 {
	nBins := p.NFFT/2 + 1
	freqs := make([]float64, nBins)
	for k := range freqs {
		freqs[k] = float64(k) * audio.SampleRate / float64(p.NFFT)
	}
	center := func(i int) float64 { // i = -1 .. NBands
		return p.FMin * math.Pow(2, float64(i)/float64(p.BandsPerOctave))
	}
	fb := make([][]float64, p.NBands)
	for b := range fb {
		lo, c, hi := center(b-1), center(b), center(b+1)
		row := make([]float64, nBins)
		sum := 0.0
		for k, f := range freqs {
			v := math.Max(math.Min((f-lo)/(c-lo), (hi-f)/(hi-c)), 0)
			row[k] = v
			sum += v
		}
		if sum == 0 {
			nearest := 0
			for k, f := range freqs {
				if math.Abs(f-c) < math.Abs(freqs[nearest]-c) {
					nearest = k
				}
			}
			row[nearest], sum = 1, 1
		}
		for k := range row {
			row[k] /= sum
		}
		fb[b] = row
	}
	return fb
}

func sparse(fb [][]float64) []band {
	out := make([]band, len(fb))
	for b, row := range fb {
		lo, hi := -1, -1
		for k, v := range row {
			if v != 0 {
				if lo < 0 {
					lo = k
				}
				hi = k
			}
		}
		out[b] = band{Lo: lo, W: row[lo : hi+1]}
	}
	return out
}

// Fingerprinter holds the FFT plan, window, filterbank, and scratch. It is not
// safe for concurrent use: give each goroutine its own.
type Fingerprinter struct {
	p      Params
	half   []band // HalfBand(p)'s filterbank, built on first FingerprintQuery (D35)
	fft    *fourier.FFT
	window []float64
	bands  []band
	frame  []float64
	coeffs []complex128
	mag    []float64
}

// New returns a Fingerprinter for p.
func New(p Params) *Fingerprinter {
	w := make([]float64, p.NFFT)
	for n := range w { // numpy.hanning: symmetric
		w[n] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(n)/float64(p.NFFT-1))
	}
	return &Fingerprinter{
		p: p, fft: fourier.NewFFT(p.NFFT), window: w, bands: sparse(Filterbank(p)),
		frame: make([]float64, p.NFFT), coeffs: make([]complex128, p.NFFT/2+1),
		mag: make([]float64, p.NFFT/2+1),
	}
}

// Spectrogram computes the log-frequency magnitude in dB: frame k starts at
// sample k*Hop, no centering or padding.
func (fp *Fingerprinter) Spectrogram(pcm []float32) fingerprint.Spectrogram {
	return fp.spectrograms(pcm, fp.bands)[0]
}

// spectrograms computes pcm's spectrogram on each filterbank from one FFT
// per frame (D44): the two filterbanks of a query (D35) share NFFT, Hop, and
// window, so only the band sums differ. Each result equals that
// filterbank's Spectrogram exactly.
func (fp *Fingerprinter) spectrograms(pcm []float32, banks ...[]band) []fingerprint.Spectrogram {
	p := fp.p
	out := make([]fingerprint.Spectrogram, len(banks))
	if len(pcm) < p.NFFT {
		for i := range out {
			out[i] = fingerprint.Spectrogram{Bins: p.NBands}
		}
		return out
	}
	frames := (len(pcm)-p.NFFT)/p.Hop + 1
	for i := range out {
		out[i] = fingerprint.Spectrogram{Frames: frames, Bins: p.NBands, Data: make([]float64, frames*p.NBands)}
	}
	for t := 0; t < frames; t++ {
		start := t * p.Hop
		for n := range fp.frame {
			fp.frame[n] = float64(pcm[start+n]) * fp.window[n]
		}
		fp.coeffs = fp.fft.Coefficients(fp.coeffs, fp.frame)
		for k, c := range fp.coeffs {
			fp.mag[k] = cmplx.Abs(c)
		}
		for i, bands := range banks {
			row := out[i].Data[t*p.NBands : (t+1)*p.NBands]
			for b, bd := range bands {
				s := 0.0
				for j, w := range bd.W {
					s += w * fp.mag[bd.Lo+j]
				}
				row[b] = 20 * math.Log10(s+1e-10)
			}
		}
	}
	return out
}

func (p Params) peakParams() fingerprint.Params {
	return fingerprint.Params{
		NbhdT: p.NbhdT, NbhdF: p.NbhdF, FloorDB: p.FloorDB,
		BlockFrames: p.BlockFrames, PeaksPerBlock: p.PeaksPerBlock,
	}
}

// Triplets builds the triplet landmarks of peaks sorted by (t, f), in
// prototype.b2.triplets order: by anchor time, then hash (stable from
// generation order: target pair (a, b) major, anchor minor).
func Triplets(peaks []fingerprint.Peak, p Params) []Landmark {
	n := len(peaks)
	tgt := make([][]int, n)
	for i, a := range peaks {
		for j := i + 1; j < n && len(tgt[i]) < p.Fanout; j++ {
			dt := peaks[j].T - a.T
			if dt > p.MaxDT {
				break
			}
			if dt >= 1 && abs(peaks[j].F-a.F) <= p.Zone {
				tgt[i] = append(tgt[i], j)
			}
		}
	}
	var out []Landmark
	for a := 0; a < p.Fanout; a++ {
		for b := a + 1; b < p.Fanout; b++ {
			for i := range peaks {
				if len(tgt[i]) <= b {
					continue
				}
				pi, pj, pk := peaks[i], peaks[tgt[i][a]], peaks[tgt[i][b]]
				if pk.T <= pj.T {
					continue
				}
				span := pk.T - pi.T
				rt := (pj.T - pi.T) * p.RTLevels / span
				h := uint32(pi.F/p.BandWidth)<<20 | uint32(rt)<<14 |
					uint32(pj.F-pi.F+p.Zone)<<7 | uint32(pk.F-pi.F+p.Zone)
				out = append(out, Landmark{Hash: h, T: uint32(pi.T), Span: uint8(span), Band: uint8(pi.F)})
			}
		}
	}
	sort.SliceStable(out, func(x, y int) bool {
		if out[x].T != out[y].T {
			return out[x].T < out[y].T
		}
		return out[x].Hash < out[y].Hash
	})
	return out
}

// Fingerprint returns the triplet landmarks of pcm.
func (fp *Fingerprinter) Fingerprint(pcm []float32) []Landmark {
	return Triplets(fingerprint.FindPeaks(fp.Spectrogram(pcm), fp.p.peakParams()), fp.p)
}

// HalfBand returns p with every filterbank center half a band higher (D35).
func HalfBand(p Params) Params {
	p.FMin *= math.Pow(2, 1/(2*float64(p.BandsPerOctave)))
	return p
}

// FingerprintQuery fingerprints pcm on nShifts sub-hop grids (D15), on both
// this filterbank and the HalfBand one (D35: a turntable pitch shift near
// half a band still lines up with one of them), and returns the union
// sorted by (Hash, T, Span, Band) without exact duplicates, as
// prototype.b2.fingerprint_query does. The index side uses Fingerprint.
func (fp *Fingerprinter) FingerprintQuery(pcm []float32, nShifts int) []Landmark {
	if fp.half == nil {
		fp.half = sparse(Filterbank(HalfBand(fp.p)))
	}
	step := fp.p.Hop / nShifts
	var all []Landmark
	for k := 0; k < nShifts; k++ {
		if k*step >= len(pcm) {
			break
		}
		for _, s := range fp.spectrograms(pcm[k*step:], fp.bands, fp.half) {
			all = append(all, Triplets(fingerprint.FindPeaks(s, fp.p.peakParams()), fp.p)...)
		}
	}
	key := func(l Landmark) uint64 {
		return uint64(l.Hash)<<32 | uint64(l.T)<<16 | uint64(l.Span)<<8 | uint64(l.Band)
	}
	sort.Slice(all, func(i, j int) bool { return key(all[i]) < key(all[j]) })
	out := all[:0]
	for i, l := range all {
		if i == 0 || l != all[i-1] {
			out = append(out, l)
		}
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
