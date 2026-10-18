"""Tempo-invariant matcher for B2 triplets (D31 option B2): Python reference.

Index: postings sorted by hash, each (track, t, span, band) from b2.Landmarks.

Search, in two stages:
1. Coarse votes. Every (query landmark, posting) pair with equal hashes is a
   match. Its time scale is reference span / query span; with spans counted
   in whole frames that ratio is only known to within (span_r +/- 0.5) /
   (span_q -/+ 0.5), so the match votes in each log-spaced scale bin that
   interval covers (bins SCALE_STEP wide over SCALE_RANGE). Within a bin, its
   offset is t_ref - scale * t_query at the bin's center scale, in
   OFFSET_FRAMES-wide bins. A track's coarse score is its best
   (scale bin, offset bin) count.
2. Refine. For the top tracks, take the matches near the winning bin and fit
   t_ref = offset + scale * t_query by weighted least squares, keep matches
   within INLIER_FRAMES of the line, refit once. The score is the number of
   inlier query landmarks (each counted once).

Returns candidates (track, score, offset in frames at query time 0, scale).
"""

from dataclasses import dataclass

import numpy as np

from prototype.b2 import Landmarks

SCALE_RANGE = (0.84, 1.19)  # time scales considered, about -16% to +19%
SCALE_STEP = np.log2(1.02)  # coarse scale bin width, 2%
OFFSET_FRAMES = 6  # coarse offset bin width
INLIER_FRAMES = 2.0
REFINE_TRACKS = 20
MAX_BINS_PER_MATCH = 4


@dataclass(frozen=True)
class Index:
    keys: np.ndarray  # uint32, sorted unique hashes
    offsets: np.ndarray  # int64, len(keys) + 1
    track: np.ndarray  # uint32
    t: np.ndarray  # uint32
    span: np.ndarray  # uint8
    band: np.ndarray  # uint8

    @property
    def n_postings(self) -> int:
        return len(self.track)


def build(fingerprints: list[Landmarks]) -> Index:
    """Index fingerprints; fingerprints[d] belongs to dense track id d."""
    h = np.concatenate([fp.hash for fp in fingerprints])
    tr = np.concatenate([np.full(len(fp.hash), d, np.uint32) for d, fp in enumerate(fingerprints)])
    t = np.concatenate([fp.t for fp in fingerprints])
    span = np.concatenate([fp.span for fp in fingerprints])
    band = np.concatenate([fp.band for fp in fingerprints])
    order = np.lexsort((t, tr, h))
    h, tr, t, span, band = h[order], tr[order], t[order], span[order], band[order]
    keys, starts = np.unique(h, return_index=True)
    offsets = np.append(starts, len(h)).astype(np.int64)
    return Index(keys, offsets, tr, t, span, band)


@dataclass(frozen=True)
class Candidate:
    track: int
    score: int
    offset: float  # reference frame at query frame 0
    scale: float  # reference time per query time (> 1: the query plays faster)


@dataclass(frozen=True)
class Result:
    candidates: list[Candidate]
    postings_scanned: int
    votes: int


def matches(index: Index, q: Landmarks) -> tuple[np.ndarray, ...]:
    """All (query landmark, posting) pairs with equal hashes, as arrays:
    (query index, track, t_query, t_ref, span_query, span_ref)."""
    pos = np.searchsorted(index.keys, q.hash)
    pos_c = np.minimum(pos, len(index.keys) - 1)
    hit = (pos < len(index.keys)) & (index.keys[pos_c] == q.hash)
    qi = np.nonzero(hit)[0]
    a, b = index.offsets[pos_c[qi]], index.offsets[pos_c[qi] + 1]
    counts = b - a
    rep = np.repeat(qi, counts)
    post = np.repeat(a - np.cumsum(counts) + counts, counts) + np.arange(counts.sum())
    return (rep, index.track[post].astype(np.int64), q.t[rep].astype(np.float64),
            index.t[post].astype(np.float64), q.span[rep].astype(np.float64),
            index.span[post].astype(np.float64))  # fmt: skip


def search(index: Index, q: Landmarks, top_k: int = 20) -> Result:
    if len(q.hash) == 0 or index.n_postings == 0:
        return Result([], 0, 0)
    qi, track, tq, tr, sq, sr = matches(index, q)
    if len(qi) == 0:
        return Result([], 0, 0)
    # Stage 1: each match votes in the scale bins its span ratio allows.
    lo = np.log2(np.maximum(sr - 0.5, 0.5) / (sq + 0.5)) / SCALE_STEP
    hi = np.log2((sr + 0.5) / np.maximum(sq - 0.5, 0.5)) / SCALE_STEP
    b_min, b_max = (int(np.floor(np.log2(x) / SCALE_STEP)) for x in SCALE_RANGE)
    first = np.clip(np.floor(lo), b_min, b_max).astype(np.int64)
    last = np.clip(np.ceil(hi), b_min, b_max).astype(np.int64)
    n_bins = np.minimum(last - first + 1, MAX_BINS_PER_MATCH)
    keep = (hi >= b_min) & (lo <= b_max)
    center = (lo + hi) / 2
    first = np.where(n_bins < last - first + 1, np.round(center - (n_bins - 1) / 2), first).astype(
        np.int64
    )
    idx = np.nonzero(keep)[0]
    n = n_bins[idx]
    rep = np.repeat(idx, n)
    bins = np.repeat(first[idx], n) + (np.arange(n.sum()) - np.repeat(np.cumsum(n) - n, n))
    scale = 2.0 ** (bins * SCALE_STEP)
    off = np.floor((tr[rep] - scale * tq[rep]) / OFFSET_FRAMES).astype(np.int64)
    key = (track[rep] << 40) | ((bins - b_min) << 32) | (off + (1 << 31))
    uk, counts = np.unique(key, return_counts=True)
    trk = uk >> 40
    best_per_track: dict[int, tuple[int, int]] = {}
    order = np.argsort(-counts, kind="stable")
    for u in order:
        tk = int(trk[u])
        if tk not in best_per_track:
            best_per_track[tk] = (int(counts[u]), int(uk[u]))
        if len(best_per_track) >= REFINE_TRACKS:
            break
    # Stage 2: line fit per top track around its winning coarse bin.
    cands = []
    for tk, (_, k) in best_per_track.items():
        b = ((k >> 32) & 0xFF) + b_min
        o = (k & 0xFFFFFFFF) - (1 << 31)
        s0 = 2.0 ** (b * SCALE_STEP)
        m = track == tk
        resid = tr[m] - (s0 * tq[m] + (o + 0.5) * OFFSET_FRAMES)
        near = np.abs(resid) <= OFFSET_FRAMES * 2
        t_q, t_r, q_i = tq[m][near], tr[m][near], qi[m][near]
        if len(t_q) < 2 or np.ptp(t_q) == 0:
            continue
        s, a = np.polyfit(t_q, t_r, 1)
        for _ in range(2):
            inl = np.abs(t_r - (a + s * t_q)) <= INLIER_FRAMES
            if inl.sum() < 2 or np.ptp(t_q[inl]) == 0:
                break
            s, a = np.polyfit(t_q[inl], t_r[inl], 1)
        inl = np.abs(t_r - (a + s * t_q)) <= INLIER_FRAMES
        cands.append(Candidate(tk, int(len(np.unique(q_i[inl]))), float(a), float(s)))
    cands.sort(key=lambda c: (-c.score, c.track))
    return Result(cands[:top_k], postings_scanned=len(qi), votes=len(rep))
