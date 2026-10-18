"""Tempo-invariant triplet fingerprint (D31 option B2, D33): Python reference.

Front end (D33): a log-frequency spectrogram, a 4096-sample STFT (hop 256,
11,025 Hz) through a triangular filterbank of 36 bands per octave from 55 Hz.
On a log axis a turntable tempo change (pitch moves with tempo) slides every
peak by the same number of bands, and key lock leaves them in place.

Peaks: the same rule as the pair fingerprint (D9): local maxima of a
17-frame x 9-band neighborhood above an absolute floor, then the strongest 30
per 43-frame block.

Triplets: each peak (the anchor, i) takes its next FANOUT peaks within 1 to
63 frames and +/-36 bands (one octave); every pair (j, k) of those targets in
time order, with t_j < t_k, forms a triplet. The hash keeps what a tempo
change cannot move, plus a coarse pitch band for specificity:

  band  4 bits  anchor band // 18 (half-octave; a +/-8% turntable change
                moves it by at most 4 bands, so crossings are rare)
  rt    6 bits  floor(64 * (t_j - t_i) / (t_k - t_i)), the time-gap ratio
  dfj   7 bits  f_j - f_i + 36 (bands; a pitch shift slides all three)
  dfk   7 bits  f_k - f_i + 36

  hash = band << 20 | rt << 14 | dfj << 7 | dfk   (24 bits)

Each landmark also carries what the matcher needs to estimate a match's
tempo factor: span = t_k - t_i (frames) and the anchor band. Reference span
over query span is the time scale; the anchor band difference is the pitch
shift (turntable) or 0 (key lock).
"""

from dataclasses import dataclass, replace
from functools import cache

import numpy as np
from numpy.lib.stride_tricks import sliding_window_view

from prototype.audio import SAMPLE_RATE
from prototype.fingerprint import find_peaks


@dataclass(frozen=True)
class Params:
    n_fft: int = 4096
    hop: int = 256
    bands_per_octave: int = 36
    fmin: float = 55.0
    n_bands: int = 234  # 36 * log2(5000 / 55), up to about 5 kHz
    nbhd_t: int = 17  # max-filter neighborhood, frames
    nbhd_f: int = 9  # max-filter neighborhood, bands (1/4 octave)
    floor_db: float = -8.0  # absolute floor; rejects near-silence
    block_frames: int = 43
    peaks_per_block: int = 30
    fanout: int = 5
    max_dt: int = 63
    zone: int = 36  # max |band gap| to a target (one octave)
    band_width: int = 18  # bands per hash pitch band (half an octave)
    rt_levels: int = 64


DEFAULT = Params()


@cache
def filterbank(n_fft: int, bpo: int, fmin: float, n_bands: int) -> np.ndarray:
    """Triangular filters centered every 1/bpo octave from fmin over rfft bins,
    each normalized to sum 1. A band too narrow to hold any FFT bin uses the
    nearest one."""
    freqs = np.fft.rfftfreq(n_fft, 1 / SAMPLE_RATE)
    centers = fmin * 2.0 ** (np.arange(-1, n_bands + 1) / bpo)
    lo, c, hi = centers[:-2, None], centers[1:-1, None], centers[2:, None]
    fb = np.clip(np.minimum((freqs - lo) / (c - lo), (hi - freqs) / (hi - c)), 0, None)
    empty = fb.sum(axis=1) == 0
    nearest = np.argmin(np.abs(freqs[None, :] - c), axis=1)
    fb[np.nonzero(empty)[0], nearest[empty]] = 1.0
    return fb / fb.sum(axis=1, keepdims=True)


def spectrogram(pcm: np.ndarray, p: Params = DEFAULT) -> np.ndarray:
    """Log-frequency magnitude in dB, float64, shape (frames, n_bands)."""
    if len(pcm) < p.n_fft:
        return np.empty((0, p.n_bands), dtype=np.float64)
    frames = sliding_window_view(pcm.astype(np.float64), p.n_fft)[:: p.hop]
    mag = np.abs(np.fft.rfft(frames * np.hanning(p.n_fft), axis=1))
    fb = filterbank(p.n_fft, p.bands_per_octave, p.fmin, p.n_bands)
    return 20 * np.log10(mag @ fb.T + 1e-10)


@dataclass(frozen=True)
class Landmarks:
    hash: np.ndarray  # uint32, 24 bits used
    t: np.ndarray  # uint32, anchor frame
    span: np.ndarray  # uint8, t_k - t_i in frames
    band: np.ndarray  # uint8, anchor band


def _empty() -> Landmarks:
    z = np.empty(0, dtype=np.uint32)
    return Landmarks(z, z, z.astype(np.uint8), z.astype(np.uint8))


def triplets(
    t: np.ndarray, f: np.ndarray, p: Params = DEFAULT, fpos: np.ndarray | None = None
) -> Landmarks:
    """Triplet landmarks from peaks sorted by (t, f). With fpos (fractional
    band positions of the same peaks; experiments only), the hash's band
    differences are rint(fpos[j] - fpos[i]) instead of f[j] - f[i]."""
    n = len(t)
    if n < 3:
        return _empty()
    reach = np.searchsorted(t, t + p.max_dt, side="right") - np.arange(n) - 1
    width = int(reach.max())
    if width < 2:
        return _empty()
    anchor = np.arange(n)[:, None]
    cand = np.minimum(anchor + np.arange(1, width + 1)[None, :], n - 1)
    in_range = (anchor + np.arange(1, width + 1)[None, :]) < n
    dt = t[cand] - t[anchor]
    valid = in_range & (dt >= 1) & (dt <= p.max_dt) & (np.abs(f[cand] - f[anchor]) <= p.zone)
    valid &= np.cumsum(valid, axis=1) <= p.fanout
    # Targets per anchor, left-packed: tgt[i, :p.fanout], -1 where fewer.
    order = np.argsort(~valid, axis=1, kind="stable")[:, : p.fanout]
    tgt = np.where(
        np.take_along_axis(valid, order, axis=1), np.take_along_axis(cand, order, axis=1), -1
    )
    if tgt.shape[1] < p.fanout:  # fewer candidates than the fanout anywhere: pad
        tgt = np.pad(tgt, ((0, 0), (0, p.fanout - tgt.shape[1])), constant_values=-1)
    hs, ts, spans, bands = [], [], [], []
    for a in range(p.fanout):
        for b in range(a + 1, p.fanout):
            j, k = tgt[:, a], tgt[:, b]
            ok = (j >= 0) & (k >= 0)
            i = np.nonzero(ok)[0]
            j, k = j[ok], k[ok]
            ok2 = t[k] > t[j]
            i, j, k = i[ok2], j[ok2], k[ok2]
            span = t[k] - t[i]
            rt = ((t[j] - t[i]) * p.rt_levels) // span
            band = f[i] // p.band_width
            if fpos is None:
                dj, dk = f[j] - f[i], f[k] - f[i]
            else:
                dj = np.clip(np.rint(fpos[j] - fpos[i]), -p.zone, p.zone).astype(np.int64)
                dk = np.clip(np.rint(fpos[k] - fpos[i]), -p.zone, p.zone).astype(np.int64)
            h = (band << 20) | (rt << 14) | ((dj + p.zone) << 7) | (dk + p.zone)
            hs.append(h)
            ts.append(t[i])
            spans.append(span)
            bands.append(f[i])
    h = np.concatenate(hs)
    tt = np.concatenate(ts)
    order = np.lexsort((h, tt))  # deterministic order: by anchor time, then hash
    span, band = np.concatenate(spans), np.concatenate(bands)
    return Landmarks(
        h[order].astype(np.uint32),
        tt[order].astype(np.uint32),
        span[order].astype(np.uint8),
        band[order].astype(np.uint8),
    )


def fingerprint(pcm: np.ndarray, p: Params = DEFAULT) -> Landmarks:
    pk = find_peaks(spectrogram(pcm, p), p)  # uses nbhd_t, nbhd_f, floor_db, block, per-block
    return triplets(pk.t, pk.f, p)


def half_band(p: Params = DEFAULT) -> Params:
    """p with every filterbank center half a band higher (D35)."""
    return replace(p, fmin=p.fmin * 2 ** (1 / (2 * p.bands_per_octave)))


def fingerprint_query(
    pcm: np.ndarray, n_shifts: int = 4, p: Params = DEFAULT, dual: bool = True
) -> Landmarks:
    """Query-side fingerprint over n_shifts sub-hop offsets (as D15) and, with
    dual (D35), on both p's filterbank and half_band(p)'s, so a turntable
    pitch shift near half a band still lines up with one of them; exact
    duplicate landmarks dropped. The index side uses fingerprint (p only)."""
    step = p.hop // n_shifts
    banks = [p, half_band(p)] if dual else [p]
    parts = [fingerprint(pcm[k * step :], q) for q in banks for k in range(n_shifts)]
    h = np.concatenate([x.hash for x in parts]).astype(np.uint64)
    t = np.concatenate([x.t for x in parts]).astype(np.uint64)
    span = np.concatenate([x.span for x in parts]).astype(np.uint64)
    band = np.concatenate([x.band for x in parts]).astype(np.uint64)
    packed = np.unique((h << np.uint64(32)) | (t << np.uint64(16)) | (span << np.uint64(8)) | band)
    return Landmarks(
        (packed >> np.uint64(32)).astype(np.uint32),
        ((packed >> np.uint64(16)) & np.uint64(0xFFFF)).astype(np.uint32),
        ((packed >> np.uint64(8)) & np.uint64(0xFF)).astype(np.uint8),
        (packed & np.uint64(0xFF)).astype(np.uint8),
    )
