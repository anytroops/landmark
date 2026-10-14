from collections import Counter, defaultdict

import numpy as np
import pytest

from prototype import fingerprint as fp
from prototype.index import Index, build
from prototype.match import Candidate, Result, accept, search

SR = 11025


def random_tracks(n: int, n_lm: int, n_keys: int, seed: int) -> list[tuple[np.ndarray, np.ndarray]]:
    rng = np.random.default_rng(seed)
    return [
        (
            rng.integers(0, n_keys, n_lm).astype(np.uint32),
            rng.integers(0, 1000, n_lm).astype(np.uint32),
        )
        for _ in range(n)
    ]


def brute_force_scores(tracks, hashes, times) -> dict[int, int]:
    """Reference matcher: dict index, Counter over (track, offset)."""
    inv: dict[int, list[tuple[int, int]]] = defaultdict(list)
    for tid, (h, t) in enumerate(tracks):
        for hh, tt in zip(h.tolist(), t.tolist(), strict=True):
            inv[hh].append((tid, tt))
    votes: Counter[tuple[int, int]] = Counter()
    for hq, tq in zip(hashes.tolist(), times.tolist(), strict=True):
        for tid, tdb in inv.get(hq, []):
            votes[(tid, tdb - tq)] += 1
    best: dict[int, int] = {}
    for (tid, _), c in votes.items():
        best[tid] = max(best.get(tid, 0), c)
    return best


def test_build_layout_invariants() -> None:
    tracks = random_tracks(5, 200, 50, seed=0)
    idx = build(tracks, [10, 20, 30, 40, 50])
    assert idx.n_postings == 1000
    assert np.all(np.diff(idx.keys.astype(np.int64)) > 0)
    assert idx.offsets[0] == 0 and idx.offsets[-1] == idx.n_postings
    for k in range(len(idx.keys)):
        lo, hi = idx.offsets[k], idx.offsets[k + 1]
        pair = idx.track[lo:hi].astype(np.int64) << 32 | idx.t[lo:hi]
        assert np.all(np.diff(pair) >= 0)


def test_save_load_roundtrip(tmp_path) -> None:
    idx = build(random_tracks(3, 50, 20, seed=1), [1, 2, 3])
    path = tmp_path / "idx.npz"
    idx.save(path)
    got = Index.load(path)
    for name in ("keys", "offsets", "track", "t", "fma_ids", "cluster"):
        assert np.array_equal(getattr(idx, name), getattr(got, name))


@pytest.mark.parametrize("seed", [0, 1, 2])
def test_search_matches_brute_force(seed: int) -> None:
    tracks = random_tracks(8, 300, 64, seed=seed)
    idx = build(tracks, list(range(8)))
    rng = np.random.default_rng(seed + 100)
    hashes = rng.integers(0, 64, 100).astype(np.uint32)
    times = rng.integers(0, 1000, 100).astype(np.uint32)
    got = {c.track: c.score for c in search(idx, hashes, times, top_k=8).candidates}
    assert got == brute_force_scores(tracks, hashes, times)


def test_search_empty_cases() -> None:
    idx = build(random_tracks(2, 10, 5, seed=0), [1, 2])
    miss = search(idx, np.array([999], np.uint32), np.array([0], np.uint32))
    assert miss.candidates == [] and miss.postings_scanned == 0
    empty = build([(np.empty(0, np.uint32), np.empty(0, np.uint32))], [1])
    assert search(empty, np.array([1], np.uint32), np.array([0], np.uint32)).candidates == []


def test_end_to_end_identifies_excerpt_with_offset() -> None:
    rng = np.random.default_rng(0)
    clips = [
        np.convolve(rng.standard_normal(20 * SR), np.ones(4) / 4, mode="same").astype(np.float32)
        for _ in range(5)
    ]
    idx = build([fp.fingerprint(c) for c in clips], [100, 200, 300, 400, 500])
    shift = 200  # frames
    q = clips[3][shift * 256 : shift * 256 + 5 * SR]
    res = search(idx, *fp.fingerprint(q))
    assert res.candidates[0].track == 3
    assert res.candidates[0].offset == shift
    assert accept(res, t_abs=10, t_ratio=2.0)


@pytest.mark.parametrize(
    ("scores", "runner_up", "t_abs", "t_ratio", "want"),
    [
        ([], 0, 1, 1.0, False),
        ([50], 0, 10, 2.0, True),
        ([50, 30], 30, 10, 2.0, False),
        ([50, 20], 20, 10, 2.0, True),
        ([8, 1], 1, 10, 2.0, False),
        ([50, 50], 3, 10, 2.0, True),  # second is a duplicate; runner-up is outside cluster
    ],
)
def test_accept(scores: list[int], runner_up: int, t_abs: int, t_ratio: float, want: bool) -> None:
    res = Result([Candidate(i, s, 0) for i, s in enumerate(scores)], 0, runner_up)
    assert accept(res, t_abs, t_ratio) is want


def test_runner_up_skips_duplicate_cluster() -> None:
    rng = np.random.default_rng(0)
    base = (rng.integers(0, 1 << 20, 400).astype(np.uint32), np.arange(400, dtype=np.uint32))
    other = (rng.integers(0, 1 << 20, 400).astype(np.uint32), np.arange(400, dtype=np.uint32))
    tracks = [base, base, other]  # tracks 0 and 1 are identical copies
    q_h, q_t = base[0][100:200], base[1][100:200] - 100

    plain = search(build(tracks, [10, 11, 12]), q_h, q_t)
    assert plain.runner_up == 100  # the copy counts as a competitor
    assert not accept(plain, t_abs=10, t_ratio=2.0)

    dedup = search(build(tracks, [10, 11, 12], clusters=[[10, 11]]), q_h, q_t)
    assert dedup.candidates[0].score == 100
    assert dedup.runner_up < 10
    assert accept(dedup, t_abs=10, t_ratio=2.0)
