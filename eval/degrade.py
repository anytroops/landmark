"""Query degradations: excerpting, additive noise, MP3 re-encode, speed change."""

import numpy as np
from scipy.signal import resample_poly

from prototype.audio import SAMPLE_RATE, encode_decode_mp3


def excerpt(
    pcm: np.ndarray, seconds: float, rng: np.random.Generator, sample_rate: int = SAMPLE_RATE
) -> tuple[np.ndarray, int]:
    """Random excerpt of `seconds`. Returns (samples, start offset in samples)."""
    n = int(round(seconds * sample_rate))
    if len(pcm) < n:
        raise ValueError(f"clip has {len(pcm)} samples, need {n}")
    start = int(rng.integers(0, len(pcm) - n + 1))
    return pcm[start : start + n].copy(), start


def add_noise(
    pcm: np.ndarray, snr_db: float, rng: np.random.Generator, noise: np.ndarray | None = None
) -> np.ndarray:
    """Add noise scaled to `snr_db` relative to the signal power.

    With `noise=None`, uses white Gaussian noise. Otherwise a random window of
    `noise` (tiled if shorter than the signal).
    """
    if noise is None:
        n = rng.standard_normal(len(pcm))
    else:
        reps = int(np.ceil(len(pcm) / len(noise))) + 1
        tiled = np.tile(noise, reps)
        start = int(rng.integers(0, len(tiled) - len(pcm) + 1))
        n = tiled[start : start + len(pcm)].astype(np.float64)
    p_sig = float(np.mean(pcm.astype(np.float64) ** 2))
    p_noise = float(np.mean(n**2))
    if p_sig == 0 or p_noise == 0:
        return pcm.copy()
    scale = np.sqrt(p_sig / (p_noise * 10 ** (snr_db / 10)))
    return (pcm + scale * n).astype(np.float32)


def reencode(pcm: np.ndarray, bitrate: str) -> np.ndarray:
    return encode_decode_mp3(pcm, bitrate)


def speed(pcm: np.ndarray, factor: float) -> np.ndarray:
    """Turntable-style speed change: tempo and pitch both scale by `factor`."""
    up = 1000
    down = int(round(up * factor))
    return resample_poly(pcm, up, down).astype(np.float32)
