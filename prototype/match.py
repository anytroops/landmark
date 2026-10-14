"""Offset-histogram matching.

For each query landmark (h, tq) and each posting (track, tdb) under h, vote
for (track, tdb - tq). A track's score is its largest vote count: the number
of landmarks that agree on one time alignment.

The runner-up for the acceptance ratio is the best track outside the top-1's
duplicate cluster (D14): a duplicate scoring as high as the original is
agreement, not ambiguity.
"""

from dataclasses import dataclass

import numpy as np

from prototype.index import Index

# (track, offset) packed into one int64: track << 32 | (offset + 2^31).
_OFF_BIAS = 1 << 31


@dataclass(frozen=True)
class Candidate:
    track: int  # dense id
    score: int
    offset: int  # tdb - tq, frames


@dataclass(frozen=True)
class Result:
    candidates: list[Candidate]  # descending score
    postings_scanned: int
    runner_up: int = 0  # best score outside the top-1's duplicate cluster


def gather(index: Index, hashes: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
    """Posting index ranges for each query hash: (query position per posting, posting index)."""
    pos = np.searchsorted(index.keys, hashes)
    pos_c = np.minimum(pos, max(len(index.keys) - 1, 0))
    hit = (pos < len(index.keys)) & (index.keys[pos_c] == hashes) if len(index.keys) else pos < 0
    q = np.nonzero(hit)[0]
    lo = index.offsets[pos[q]]
    n = index.offsets[pos[q] + 1] - lo
    total = int(n.sum())
    # Flatten ranges [lo_i, lo_i + n_i) without a Python loop.
    qpos = np.repeat(q, n)
    within = np.arange(total) - np.repeat(np.cumsum(n) - n, n)
    return qpos, np.repeat(lo, n) + within


def search(index: Index, hashes: np.ndarray, times: np.ndarray, top_k: int = 10) -> Result:
    qpos, post = gather(index, hashes)
    if len(post) == 0:
        return Result([], 0)
    offset = index.t[post].astype(np.int64) - times[qpos].astype(np.int64)
    key = (index.track[post].astype(np.int64) << 32) | (offset + _OFF_BIAS)
    pairs, counts = np.unique(key, return_counts=True)
    tracks = pairs >> 32

    # Best offset per track: pairs are sorted by track, so take the max within each run.
    order = np.lexsort((-counts, tracks))
    tracks, pairs, counts = tracks[order], pairs[order], counts[order]
    first = np.ones(len(tracks), dtype=bool)
    first[1:] = tracks[1:] != tracks[:-1]
    tracks, pairs, counts = tracks[first], pairs[first], counts[first]

    top = np.argsort(-counts, kind="stable")[:top_k]
    cands = [
        Candidate(int(tracks[i]), int(counts[i]), int((pairs[i] & 0xFFFFFFFF) - _OFF_BIAS))
        for i in top
    ]
    outside = index.cluster[tracks] != index.cluster[tracks[top[0]]]
    runner_up = int(counts[outside].max()) if outside.any() else 0
    return Result(cands, len(post), runner_up)


def accept(result: Result, t_abs: int, t_ratio: float) -> bool:
    """Accept top-1 if it clears t_abs and beats the out-of-cluster runner-up by t_ratio."""
    if not result.candidates:
        return False
    best = result.candidates[0].score
    return best >= t_abs and best >= t_ratio * max(result.runner_up, 1)
