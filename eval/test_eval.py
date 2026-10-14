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
    groups = [[i] for i in range(100, 190)] + [[200, 201], [202, 203, 204], [205, 206]]
    a = make_splits(groups, seed=0, n_neg=20, n_val_pos=10, n_test_pos=10)
    assert a == make_splits(groups, seed=0, n_neg=20, n_val_pos=10, n_test_pos=10)
    assert make_splits(groups, seed=1, n_neg=20, n_val_pos=10, n_test_pos=10) != a
    indexed = set(a["indexed"])
    neg = set(a["val_neg"]) | set(a["test_neg"])
    assert not indexed & neg
    assert indexed | neg == {t for g in groups for t in g}
    assert not set(a["val_neg"]) & set(a["test_neg"])
    assert not set(a["val_pos"]) & set(a["test_pos"])
    assert set(a["val_pos"]) | set(a["test_pos"]) <= indexed
    assert len(a["val_pos"]) == len(a["test_pos"]) == 10


def test_splits_keep_clusters_together() -> None:
    groups = [[i] for i in range(100, 160)] + [[i, i + 1, i + 2] for i in range(300, 360, 3)]
    for seed in range(5):
        s = make_splits(groups, seed=seed, n_neg=30, n_val_pos=10, n_test_pos=10)
        side = {t: k for k in ("indexed", "val_neg", "test_neg") for t in s[k]}
        for g in groups:
            assert len({side[t] for t in g}) == 1
        pos = s["val_pos"] + s["test_pos"]
        for g in groups:
            assert sum(t in pos for t in g) <= 1


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


@pytest.mark.skipif(shutil.which("ffmpeg") is None, reason="ffmpeg not installed")
def test_reencode_bitrates_differ() -> None:
    # Regression: at 11,025 Hz both bitrates clamped to 64k and produced identical output.
    sig = np.random.default_rng(0).standard_normal(3 * 11025).astype(np.float32) * 0.1
    a, b = reencode(sig, "128k"), reencode(sig, "64k")
    n = min(len(a), len(b))
    assert not np.array_equal(a[:n], b[:n])


def test_dense_tables_match_ingest_rules() -> None:
    from eval.backends import dense_tables

    manifest = {"indexed": [40, 10, 30, 20], "clusters": {"20": [20, 40, 999]}}
    fma, cluster = dense_tables(manifest)
    assert fma.tolist() == [10, 20, 30, 40]  # dense id = sorted position
    assert cluster.tolist() == [0, 1, 2, 1]  # 40 joins 20's cluster; 999 not indexed
