import shutil

import numpy as np
import pytest

from eval.degrade import add_noise, excerpt, reencode, speed
from eval.splits import make_splits
from eval.stats import wilson


@pytest.mark.parametrize(
    ("k", "n", "lo", "hi"),
    [
        (0, 10, 0.0, 0.2775),
        (10, 10, 0.7225, 1.0),
        (50, 100, 0.4038, 0.5962),
        (950, 1000, 0.9346, 0.9619),
    ],
)
def test_wilson_known_values(k: int, n: int, lo: float, hi: float) -> None:
    got_lo, got_hi = wilson(k, n)
    assert got_lo == pytest.approx(lo, abs=1e-4)
    assert got_hi == pytest.approx(hi, abs=1e-4)


@pytest.mark.parametrize(("k", "n"), [(1, 0), (-1, 5), (6, 5)])
def test_wilson_rejects_bad_input(k: int, n: int) -> None:
    with pytest.raises(ValueError):
        wilson(k, n)


def test_splits_are_disjoint_and_deterministic() -> None:
    ids = list(range(100, 200))
    a = make_splits(ids, seed=0, n_neg=20, n_val_pos=10, n_test_pos=10)
    b = make_splits(ids, seed=0, n_neg=20, n_val_pos=10, n_test_pos=10)
    assert a == b
    indexed = set(a["indexed"])
    neg = set(a["val_neg"]) | set(a["test_neg"])
    assert len(indexed) == 80
    assert not indexed & neg
    assert not set(a["val_neg"]) & set(a["test_neg"])
    assert not set(a["val_pos"]) & set(a["test_pos"])
    assert set(a["val_pos"]) <= indexed
    assert set(a["test_pos"]) <= indexed
    assert make_splits(ids, seed=1, n_neg=20, n_val_pos=10, n_test_pos=10) != a


@pytest.mark.parametrize("snr_db", [15.0, 5.0, 0.0])
def test_add_noise_hits_target_snr(snr_db: float) -> None:
    rng = np.random.default_rng(0)
    sig = np.sin(2 * np.pi * 440 * np.arange(11025) / 11025).astype(np.float32)
    noisy = add_noise(sig, snr_db, rng)
    noise = noisy.astype(np.float64) - sig
    measured = 10 * np.log10(np.mean(sig.astype(np.float64) ** 2) / np.mean(noise**2))
    assert measured == pytest.approx(snr_db, abs=0.05)


def test_excerpt_length_and_bounds() -> None:
    rng = np.random.default_rng(0)
    pcm = np.arange(30 * 11025, dtype=np.float32)
    clip, start = excerpt(pcm, 10.0, rng)
    assert len(clip) == 10 * 11025
    assert clip[0] == start
    with pytest.raises(ValueError):
        excerpt(pcm[:100], 10.0, rng)


def test_speed_changes_length_and_pitch() -> None:
    sr = 11025
    sig = np.sin(2 * np.pi * 1000 * np.arange(sr) / sr).astype(np.float32)
    fast = speed(sig, 1.02)
    assert len(fast) == pytest.approx(sr / 1.02, abs=2)
    peak_hz = np.argmax(np.abs(np.fft.rfft(fast))) * sr / len(fast)
    assert peak_hz == pytest.approx(1020, abs=3)


@pytest.mark.skipif(shutil.which("ffmpeg") is None, reason="ffmpeg not installed")
def test_reencode_preserves_duration() -> None:
    sig = np.random.default_rng(0).standard_normal(5 * 11025).astype(np.float32) * 0.1
    out = reencode(sig, "64k")
    # The encoder adds ~100 ms of lead-in delay plus frame padding. Left in on
    # purpose: matching is offset-invariant, like a real query with arbitrary start.
    assert abs(len(out) - len(sig)) < 0.2 * 11025
