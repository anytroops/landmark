"""Query degradations: excerpting, additive noise, MP3 re-encode, speed change."""

from fractions import Fraction

import librosa
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
    """Turntable-style speed change: tempo and pitch both scale by `factor`.

    Resamples by the exact ratio 1/factor (to 1 part in 10,000); for the
    +/-2% conditions this is the same 50/51 and 50/49 as before.
    """
    ratio = Fraction(factor).limit_denominator(10_000)
    return resample_poly(pcm, ratio.denominator, ratio.numerator).astype(np.float32)


def stretch(pcm: np.ndarray, factor: float) -> np.ndarray:
    """Keylock-style speed change with librosa's phase vocoder: tempo scales
    by `factor`, pitch stays.

    Its phase errors accumulate along a long input, smearing spectral peaks
    a few seconds in (on one fma_medium track stretched +1.8%: 20% of the
    clean match score kept in the first 10 s, 2% from 3.3 s in), so it
    models DJ key lock poorly; see keylock() for the time-domain model.
    """
    return librosa.effects.time_stretch(pcm.astype(np.float32), rate=factor).astype(np.float32)


def keylock(pcm: np.ndarray, factor: float, frame: int = 1024, tolerance: int = 256) -> np.ndarray:
    """Keylock-style speed change by WSOLA: tempo scales by `factor`, pitch stays.

    Waveform-similarity overlap-add (Verhelst and Roelands, 1993), the family
    SoundTouch belongs to: output frames advance by frame/2; each copies a
    frame of the input from near its nominal position (advancing frame/2 *
    factor per output frame), shifted by up to +/-tolerance samples to
    maximize normalized cross-correlation with the natural continuation of
    the frame copied before (unnormalized correlation favors loud candidates
    over aligned ones), then overlap-adds it under a periodic Hann window
    (which sums to 1 at 50% overlap). Waveform pieces, and so spectral peaks,
    are kept intact. Factor 1 returns the input unchanged.
    """
    if factor == 1:
        return pcm.astype(np.float32).copy()
    x = pcm.astype(np.float64)
    hop = frame // 2
    window = 0.5 - 0.5 * np.cos(2 * np.pi * np.arange(frame) / frame)
    out_len = int(len(x) / factor)
    n_frames = max(0, (out_len - frame) // hop + 1)
    pad = tolerance + frame
    xp = np.pad(x, (pad, pad + int(hop * factor) + frame))
    y = np.zeros(n_frames * hop + frame)
    prev = pad  # padded index of the frame copied last
    for k in range(n_frames):
        nominal = pad + int(round(k * hop * factor))
        if k == 0:
            pos = nominal
        else:
            target = xp[prev + hop : prev + hop + frame]
            region = xp[nominal - tolerance : nominal + tolerance + frame]
            corr = np.correlate(region, target, mode="valid")
            energy = np.convolve(region**2, np.ones(frame), mode="valid")
            pos = nominal - tolerance + int(np.argmax(corr / np.sqrt(energy + 1e-12)))
        y[k * hop : k * hop + frame] += window * xp[pos : pos + frame]
        prev = pos
    out = np.zeros(out_len)
    n = min(out_len, len(y))
    out[:n] = y[:n]
    return out.astype(np.float32)
