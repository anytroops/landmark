"""Mixer moves (docs/design/mixer-moves.md): each playing song's gain in
the mixer's three bands over time, from the set and the songs' own audio.

B1, the non-ML baseline: per frame and per band, non-negative least squares
of the set's band powers on the playing songs' warped powers, then the same
smoothing as D43. The learned model (prototype/mixer_model.py) reads the
features built here.
"""

from dataclasses import dataclass

import numpy as np
from scipy.ndimage import median_filter, uniform_filter1d
from scipy.optimize import nnls

from eval.dj_analysis import warp

# Triplet bands (36 per octave from 55 Hz) of the mixer's three bands, less
# 8 at each end of the axis (a pitch shift moves content across the edges,
# D43): low below 250 Hz, mid to 2.5 kHz, high above.
BAND_EDGES = (8, 79, 198, 226)
SUBBANDS = 8  # per mixer band, in the model's features
FLOOR_DB = -40.0
MEDIAN_FRAMES, AVERAGE_FRAMES = 21, 43  # D43's smoothing


@dataclass
class Song:
    """One song in a set: its spectrogram (power, frames x 234), alignment,
    whether it was played turntable style (pitch moved), and the set frames
    it plays."""

    power: np.ndarray
    a: float
    rate: float
    pitch: bool
    first: int
    last: int


def warped(songs: list[Song], n: int) -> list[np.ndarray]:
    """Each song's power at the set's frames, zero outside its play."""
    out = []
    for s in songs:
        w = warp(s.power, s.a, s.rate, n, s.pitch)
        w[: max(s.first, 0)] = 0.0
        w[s.last + 1 :] = 0.0
        out.append(w)
    return out


def band_slices() -> list[slice]:
    return [slice(BAND_EDGES[b], BAND_EDGES[b + 1]) for b in range(3)]


def fit_b1(mix: np.ndarray, sources: list[np.ndarray], smooth: bool = True) -> np.ndarray:
    """Gains (songs x frames x 3 bands, linear) by per-band NNLS on the band
    powers of the songs playing in each frame; smoothed as D43 if smooth."""
    n = len(mix)
    g = np.zeros((len(sources), n, 3))
    on = np.stack([s.sum(axis=1) > 0 for s in sources], axis=1) if sources else np.zeros((n, 0))
    on = on.astype(bool)
    for t in range(n):
        idx = np.nonzero(on[t])[0]
        if len(idx) == 0:
            continue
        for b, sl in enumerate(band_slices()):
            a = np.stack([sources[s][t, sl] for s in idx], axis=1)
            coef, _ = nnls(a, mix[t, sl])
            g[idx, t, b] = np.sqrt(coef)
    if smooth:
        for s in range(len(sources)):
            for b in range(3):
                x = median_filter(g[s, :, b], size=MEDIAN_FRAMES, mode="nearest")
                g[s, :, b] = uniform_filter1d(x, AVERAGE_FRAMES, mode="nearest")
            g[s][~on[:, s]] = 0.0
    return g


def to_db(g: np.ndarray) -> np.ndarray:
    return np.maximum(20.0 * np.log10(np.maximum(g, 1e-12)), FLOOR_DB)


def subband_log(p: np.ndarray) -> np.ndarray:
    """frames x 24: log10 power in 8 sub-bands of each mixer band."""
    cols = []
    for sl in band_slices():
        for chunk in np.array_split(np.arange(sl.start, sl.stop), SUBBANDS):
            cols.append(np.log10(p[:, chunk].sum(axis=1) + 1e-10))
    return np.stack(cols, axis=1)


def features(mix: np.ndarray, sources: list[np.ndarray], b1: np.ndarray, k: int) -> np.ndarray:
    """The model's input for song k over all frames (frames x 75): the set's,
    the song's, and the other songs' sub-band log powers, and B1's gains for
    the song in dB / 20."""
    others = sum((s for j, s in enumerate(sources) if j != k), np.zeros_like(mix))
    return np.concatenate([subband_log(mix), subband_log(sources[k]), subband_log(others),
                           to_db(b1[k]) / 20.0], axis=1).astype(np.float32)  # fmt: skip


def reconstruction_error(mix: np.ndarray, sources: list[np.ndarray], g: np.ndarray) -> float:
    """Mean squared log10 distance, over frames where any song plays and the
    mixer bands' triplet bands, between the set's power and the songs'
    warped powers under gains g (songs x frames x 3)."""
    model = np.zeros_like(mix)
    for k, s in enumerate(sources):
        for b, sl in enumerate(band_slices()):
            model[:, sl] += (g[k, :, b] ** 2)[:, None] * s[:, sl]
    on = np.any(np.stack([s.sum(axis=1) > 0 for s in sources], axis=1), axis=1)
    cols = np.r_[BAND_EDGES[0] : BAND_EDGES[-1]]
    d = np.log10(mix[on][:, cols] + 1e-10) - np.log10(model[on][:, cols] + 1e-10)
    return float(np.mean(d**2))
