import numpy as np
import pytest

from prototype import fingerprint as fp

SR = 11025


def noise_clip(seconds: float, seed: int = 0) -> np.ndarray:
    # Colored-ish noise with enough spectral structure to produce peaks.
    rng = np.random.default_rng(seed)
    x = rng.standard_normal(int(seconds * SR)).astype(np.float32)
    return np.convolve(x, np.ones(4, dtype=np.float32) / 4, mode="same")


@pytest.mark.parametrize(
    ("f1", "f2", "dt"),
    [(0, 0, 1), (511, 511, 63), (100, 164, 7), (300, 236, 63)],
)
def test_hash_pack_unpack_roundtrip(f1: int, f2: int, dt: int) -> None:
    h = np.array([(f1 << 15) | (f2 << 6) | dt], dtype=np.uint32)
    got = fp.unpack(h)
    assert (got[0][0], got[1][0], got[2][0]) == (f1, f2, dt)


def test_spectrogram_shape_and_tone_bin() -> None:
    hz = 1000.0
    x = np.sin(2 * np.pi * hz * np.arange(SR) / SR).astype(np.float32)
    s = fp.spectrogram(x)
    assert s.shape == (1 + (SR - 1024) // 256, fp.N_BINS)
    assert abs(int(np.argmax(s.mean(axis=0))) - round(hz * 1024 / SR)) <= 1


def test_short_and_silent_input_produce_nothing() -> None:
    h, t = fp.fingerprint(np.zeros(500, dtype=np.float32))
    assert len(h) == len(t) == 0
    h, t = fp.fingerprint(np.zeros(5 * SR, dtype=np.float32))
    assert len(h) == len(t) == 0


def test_peak_density_capped_per_block() -> None:
    p = fp.DEFAULT
    peaks = fp.find_peaks(fp.spectrogram(noise_clip(10)), p)
    counts = np.bincount(peaks.t // p.block_frames)
    assert counts.max() <= p.peaks_per_block
    assert np.all(np.diff(peaks.t) >= 0)


def test_landmarks_respect_target_zone_and_fanout() -> None:
    p = fp.DEFAULT
    h, t1 = fp.fingerprint(noise_clip(10), p)
    assert len(h) > 0
    f1, f2, dt = fp.unpack(h)
    assert np.all((dt >= p.min_dt) & (dt <= p.max_dt))
    assert np.all(np.abs(f2 - f1) <= p.max_df)
    _, per_anchor = np.unique(np.stack([t1, f1]), axis=1, return_counts=True)
    assert per_anchor.max() <= p.fanout


def test_deterministic() -> None:
    x = noise_clip(5)
    a, b = fp.fingerprint(x), fp.fingerprint(x)
    assert np.array_equal(a[0], b[0])
    assert np.array_equal(a[1], b[1])


def test_hop_aligned_shift_preserves_hashes_and_shifts_times() -> None:
    # Shifting audio by k*hop moves every frame by exactly k. Peaks near the
    # cut and block-boundary ranking can differ, so compare the interior.
    p = fp.DEFAULT
    k = p.block_frames  # shift by whole blocks so block membership is unchanged
    x = noise_clip(12)
    h0, t0 = fp.fingerprint(x, p)
    h1, t1 = fp.fingerprint(x[k * p.hop :], p)
    a = set(zip(h0.tolist(), (t0.astype(np.int64) - k).tolist(), strict=True))
    b = set(zip(h1.tolist(), t1.astype(np.int64).tolist(), strict=True))
    jaccard = len(a & b) / len(a | b)
    assert jaccard > 0.9


def test_fingerprint_query_one_shift_equals_plain() -> None:
    x = noise_clip(5)
    h0, t0 = fp.fingerprint(x)
    h1, t1 = fp.fingerprint_query(x, n_shifts=1)
    assert set(zip(h0.tolist(), t0.tolist(), strict=True)) == set(
        zip(h1.tolist(), t1.tolist(), strict=True)
    )


def test_fingerprint_query_recovers_sub_hop_offset() -> None:
    # Index a track, query an excerpt starting mid-hop: multi-shift should
    # recover more of the track's landmarks than a single grid.
    p = fp.DEFAULT
    x = noise_clip(12, seed=3)
    track = set(zip(*(a.tolist() for a in fp.fingerprint(x, p)), strict=True))
    start = 100 * p.hop + 3 * p.hop // 4 + 5  # off the grid by ~3/4 hop
    q = x[start:]
    k_frames = start // p.hop

    def recovered(h: np.ndarray, t: np.ndarray) -> int:
        # Each query landmark at t maps to track frame t + k or t + k + 1.
        return sum(
            (hh, tt + k_frames) in track or (hh, tt + k_frames + 1) in track
            for hh, tt in zip(h.tolist(), t.tolist(), strict=True)
        )

    single = recovered(*fp.fingerprint(q, p))
    multi = recovered(*fp.fingerprint_query(q, 4, p))
    assert multi > single


def test_synthetic_golden_is_current() -> None:
    # Fails if the fingerprinter changed without `python -m prototype.golden`,
    # which would leave the Go parity test checking against stale vectors.
    import json
    from dataclasses import asdict
    from pathlib import Path

    from prototype.golden import synthetic_signal

    g = json.loads((Path(__file__).parent.parent / "testdata/golden/synthetic.json").read_text())
    assert g["params"] == asdict(fp.DEFAULT)
    h, t = fp.fingerprint(synthetic_signal(g["n_samples"]))
    assert h.tolist() == g["hashes"]
    assert t.tolist() == g["times"]
