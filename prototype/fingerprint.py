"""Landmark fingerprinting: STFT, constellation peaks, peak-pair hashes.

Pipeline (D9, D10):
  log-magnitude STFT -> 2D local maxima above an absolute floor
  -> keep the strongest N peaks per time block (exact density)
  -> pair each anchor with its next FANOUT peaks in a frequency-bounded zone
  -> hash = f1 << 15 | f2 << 6 | dt, posted at anchor frame t1.
"""

from dataclasses import dataclass

import numpy as np
from numpy.lib.stride_tricks import sliding_window_view
from scipy.ndimage import maximum_filter

F_BITS = 9
DT_BITS = 6
N_BINS = 1 << F_BITS  # 512 bins used of n_fft // 2 + 1
MAX_DT = (1 << DT_BITS) - 1  # 63 frames


@dataclass(frozen=True)
class Params:
    n_fft: int = 1024
    hop: int = 256
    nbhd_f: int = 17  # max-filter neighborhood, bins
    nbhd_t: int = 17  # max-filter neighborhood, frames
    floor_db: float = -20.0  # absolute floor; rejects near-silence
    block_frames: int = 43  # ~1 s at hop 256, 11025 Hz
    peaks_per_block: int = 30
    fanout: int = 5
    min_dt: int = 1
    max_dt: int = MAX_DT
    max_df: int = 64


DEFAULT = Params()


@dataclass(frozen=True)
class Peaks:
    t: np.ndarray  # frame index, int64, sorted by (t, f)
    f: np.ndarray  # bin index, int64
    mag: np.ndarray  # log magnitude, dB


def spectrogram(pcm: np.ndarray, p: Params = DEFAULT) -> np.ndarray:
    """Log-magnitude STFT in dB, shape (frames, N_BINS). No centering: frame k starts at k*hop."""
    if len(pcm) < p.n_fft:
        return np.empty((0, N_BINS), dtype=np.float32)
    frames = sliding_window_view(pcm.astype(np.float32), p.n_fft)[:: p.hop]
    spec = np.fft.rfft(frames * np.hanning(p.n_fft).astype(np.float32), axis=1)[:, :N_BINS]
    return (20 * np.log10(np.abs(spec) + 1e-10)).astype(np.float32)


def find_peaks(spec: np.ndarray, p: Params = DEFAULT) -> Peaks:
    """Local maxima above the floor, then the strongest `peaks_per_block` per time block."""
    if spec.size == 0:
        empty = np.empty(0, dtype=np.int64)
        return Peaks(empty, empty, np.empty(0, dtype=np.float32))
    local_max = maximum_filter(spec, size=(p.nbhd_t, p.nbhd_f), mode="constant", cval=-np.inf)
    t, f = np.nonzero((spec == local_max) & (spec > p.floor_db))
    mag = spec[t, f]

    # Rank within each block by descending magnitude; keep rank < peaks_per_block.
    block = t // p.block_frames
    order = np.lexsort((-mag, block))
    block_sorted = block[order]
    starts = np.searchsorted(block_sorted, block_sorted, side="left")
    rank = np.arange(len(order)) - starts
    keep = order[rank < p.peaks_per_block]

    keep = keep[np.lexsort((f[keep], t[keep]))]
    return Peaks(t[keep].astype(np.int64), f[keep].astype(np.int64), mag[keep])


def pair(peaks: Peaks, p: Params = DEFAULT) -> tuple[np.ndarray, np.ndarray]:
    """Landmark hashes and anchor times. Returns (hashes uint32, t1 uint32)."""
    t, f = peaks.t, peaks.f
    n = len(t)
    if n < 2:
        return np.empty(0, dtype=np.uint32), np.empty(0, dtype=np.uint32)

    # Look-ahead width: the most peaks any anchor can see inside max_dt.
    reach = np.searchsorted(t, t + p.max_dt, side="right") - np.arange(n) - 1
    width = int(reach.max())
    if width == 0:
        return np.empty(0, dtype=np.uint32), np.empty(0, dtype=np.uint32)

    # Candidate matrix: row i holds peaks i+1 .. i+width (index clipped, masked below).
    anchor = np.arange(n)[:, None]
    cand = anchor + np.arange(1, width + 1)[None, :]
    in_range = cand < n
    cand = np.minimum(cand, n - 1)
    dt = t[cand] - t[anchor]
    df = np.abs(f[cand] - f[anchor])
    valid = in_range & (dt >= p.min_dt) & (dt <= p.max_dt) & (df <= p.max_df)

    # Nearest-in-time first: keep the first `fanout` valid candidates per row.
    valid &= np.cumsum(valid, axis=1) <= p.fanout

    rows, cols = np.nonzero(valid)
    j = cand[rows, cols]
    f1, f2, d = f[rows], f[j], t[j] - t[rows]
    hashes = (f1 << (F_BITS + DT_BITS)) | (f2 << DT_BITS) | d
    return hashes.astype(np.uint32), t[rows].astype(np.uint32)


def fingerprint(pcm: np.ndarray, p: Params = DEFAULT) -> tuple[np.ndarray, np.ndarray]:
    return pair(find_peaks(spectrogram(pcm, p), p), p)


def unpack(h: np.ndarray) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    h = h.astype(np.int64)
    return h >> (F_BITS + DT_BITS), (h >> DT_BITS) & (N_BINS - 1), h & MAX_DT
