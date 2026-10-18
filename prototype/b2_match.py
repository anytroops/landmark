"""Tempo-invariant matcher for B2 triplets (D31 option B2): Python reference.

Index: postings sorted by hash, each (track, t, span, band) from b2.Landmarks.

Search, in two stages:
1. Coarse votes. Every (query landmark, posting) pair with equal hashes is a
   match. Its time scale is reference span / query span; with spans counted
   in whole frames that ratio is only known to within (span_r +/- 0.5) /
   (span_q -/+ 0.5), so the match votes in each coarse scale bin 1.02^b that
   interval reaches (at most MAX_BINS_PER_MATCH, centered on span_r /
   span_q, and only bins BIN_MIN .. BIN_MAX). Within a bin, its offset is
   t_ref - scale * t_query at the bin's scale, in OFFSET_FRAMES-wide bins. A
   track's coarse score is its best (scale bin, offset bin) count.
2. Refine. For the top tracks, take the matches near the winning bin and fit
   t_ref = offset + scale * t_query by least squares, keep matches within
   INLIER_FRAMES of the line, refit (twice). The score is the number of
   inlier query landmarks (each counted once). A fit whose scale leaves the
   searched range by more than a bin is no match (e.g. a flat line from many
   query landmarks hitting one posting) and is dropped.

Returns candidates (track, score, offset in frames at query time 0, scale).

internal/match ports this and must return the same bits, so nothing here
depends on how a language computes log or pow: the bin scales come from a
table built by repeated multiplication, the line fit from exact integer sums,
and every other float step is a single IEEE operation.
"""

from dataclasses import dataclass
from functools import cache

import numpy as np

from prototype.b2 import Landmarks

SCALE_RATIO = 1.02  # coarse scale bin b is the scale SCALE_RATIO**b
BIN_MIN, BIN_MAX = -9, 8  # bins searched: scales about 0.837 to 1.172
TABLE_BINS = 320  # scale_table covers |b| <= 320: every ratio of spans 0..255 +/- 0.5
OFFSET_FRAMES = 6  # coarse offset bin width
NEAR_FRAMES = 2 * OFFSET_FRAMES  # matches this close to the coarse line seed the fit
INLIER_FRAMES = 2.0
REFINE_TRACKS = 20
MAX_BINS_PER_MATCH = 4  # even: the window is floor_bin(ratio) - 1 .. + 2


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


@cache
def scale_table() -> np.ndarray:
    """scale_table()[b + TABLE_BINS] = SCALE_RATIO**b for |b| <= TABLE_BINS,
    by repeated multiplication and division from 1.0 (same bits in Go)."""
    up, down = [1.0], [1.0]
    for _ in range(TABLE_BINS):
        up.append(up[-1] * SCALE_RATIO)
        down.append(down[-1] / SCALE_RATIO)
    return np.array(down[:0:-1] + up)


def floor_bin(r: np.ndarray) -> np.ndarray:
    """Largest b with SCALE_RATIO**b <= r (per scale_table)."""
    return np.searchsorted(scale_table(), r, side="right") - 1 - TABLE_BINS


def ceil_bin(r: np.ndarray) -> np.ndarray:
    """Smallest b with SCALE_RATIO**b >= r (per scale_table)."""
    return np.searchsorted(scale_table(), r, side="left") - TABLE_BINS


@cache
def windows() -> tuple[np.ndarray, np.ndarray]:
    """(first, last)[span_ref, span_query]: the coarse scale bins a match votes
    in, for spans 0..255; empty (first > last) when either span is 0 or the
    bins fall outside BIN_MIN .. BIN_MAX."""
    sr, sq = np.meshgrid(np.arange(256.0), np.arange(256.0), indexing="ij")
    zero = (sr == 0) | (sq == 0)
    sr, sq = np.where(zero, 1.0, sr), np.where(zero, 1.0, sq)  # placeholders, masked below
    lo = np.maximum(sr - 0.5, 0.5) / (sq + 0.5)
    hi = (sr + 0.5) / np.maximum(sq - 0.5, 0.5)
    first, last = floor_bin(lo), ceil_bin(hi)
    wide = last - first + 1 > MAX_BINS_PER_MATCH
    centered = floor_bin(sr / sq) - (MAX_BINS_PER_MATCH // 2 - 1)
    first = np.where(wide, centered, first)
    last = np.where(wide, centered + MAX_BINS_PER_MATCH - 1, last)
    first, last = np.maximum(first, BIN_MIN), np.minimum(last, BIN_MAX)
    return np.where(zero, 1, first).astype(np.int64), np.where(zero, 0, last).astype(np.int64)


def fit_line(x: np.ndarray, y: np.ndarray) -> tuple[float, float] | None:
    """Least-squares (scale, offset) of y = offset + scale * x for integer
    frames, or None when x is constant. Sums are exact integers, then one
    rounding per float operation."""
    n = len(x)
    sx, sy = int(x.sum()), int(y.sum())
    den = n * int((x * x).sum()) - sx * sx
    if den <= 0:
        return None
    s = float(n * int((x * y).sum()) - sx * sy) / float(den)
    return s, (float(sy) - s * float(sx)) / float(n)


def matches(index: Index, q: Landmarks) -> tuple[np.ndarray, ...]:
    """All (query landmark, posting) pairs with equal hashes, as int64 arrays:
    (query index, track, t_query, t_ref, span_query, span_ref), in query
    order, then posting order."""
    pos = np.searchsorted(index.keys, q.hash)
    pos_c = np.minimum(pos, len(index.keys) - 1)
    hit = (pos < len(index.keys)) & (index.keys[pos_c] == q.hash)
    qi = np.nonzero(hit)[0]
    a, b = index.offsets[pos_c[qi]], index.offsets[pos_c[qi] + 1]
    counts = b - a
    rep = np.repeat(qi, counts)
    post = np.repeat(a - np.cumsum(counts) + counts, counts) + np.arange(counts.sum())
    i64 = np.int64
    return (rep.astype(i64), index.track[post].astype(i64), q.t[rep].astype(i64),
            index.t[post].astype(i64), q.span[rep].astype(i64),
            index.span[post].astype(i64))  # fmt: skip


def search(
    index: Index, q: Landmarks, top_k: int = 20, refine_tracks: int = REFINE_TRACKS
) -> Result:
    if len(q.hash) == 0 or index.n_postings == 0:
        return Result([], 0, 0)
    qi, track, tq, tr, sq, sr = matches(index, q)
    if len(qi) == 0:
        return Result([], 0, 0)
    tq_f, tr_f = tq.astype(np.float64), tr.astype(np.float64)
    # Stage 1: each match votes in the scale bins its span ratio allows.
    w_first, w_last = windows()
    first, last = w_first[sr, sq], w_last[sr, sq]
    n = np.maximum(last - first + 1, 0)
    idx = np.nonzero(n)[0]
    n = n[idx]
    rep = np.repeat(idx, n)
    bins = np.repeat(first[idx], n) + (np.arange(n.sum()) - np.repeat(np.cumsum(n) - n, n))
    table = scale_table()
    scale = table[bins + TABLE_BINS]
    off = np.floor((tr_f[rep] - scale * tq_f[rep]) / OFFSET_FRAMES).astype(np.int64)
    key = (track[rep] << 40) | ((bins - BIN_MIN) << 32) | ((off + (1 << 31)) & 0xFFFFFFFF)
    if len(key) == 0:
        return Result([], postings_scanned=len(qi), votes=0)
    uk, counts = np.unique(key, return_counts=True)
    trk = uk >> 40
    # Each track's best bin (ties: smallest key); tracks by count, then id.
    best_per_track: dict[int, int] = {}
    for u in np.argsort(-counts, kind="stable"):
        tk = int(trk[u])
        if tk not in best_per_track:
            best_per_track[tk] = int(uk[u])
            if len(best_per_track) >= refine_tracks:
                break
    # Stage 2: line fit per top track around its winning coarse bin.
    cands = []
    for tk, k in best_per_track.items():
        s0 = float(table[((k >> 32) & 0xFF) + BIN_MIN + TABLE_BINS])
        o = (k & 0xFFFFFFFF) - (1 << 31)
        m = track == tk
        resid = tr_f[m] - (s0 * tq_f[m] + (o + 0.5) * OFFSET_FRAMES)
        near = np.abs(resid) <= NEAR_FRAMES
        x, y, q_i = tq[m][near], tr[m][near], qi[m][near]
        x_f, y_f = x.astype(np.float64), y.astype(np.float64)
        fit = fit_line(x, y)
        if fit is None:
            continue
        s, a = fit
        for _ in range(2):
            inl = np.abs(y_f - (a + s * x_f)) <= INLIER_FRAMES
            refit = fit_line(x[inl], y[inl])
            if refit is None:
                break
            s, a = refit
        if not table[BIN_MIN - 1 + TABLE_BINS] <= s <= table[BIN_MAX + 1 + TABLE_BINS]:
            continue
        inl = np.abs(y_f - (a + s * x_f)) <= INLIER_FRAMES
        cands.append(Candidate(tk, int(len(np.unique(q_i[inl]))), a, s))
    cands.sort(key=lambda c: (-c.score, c.track))
    return Result(cands[:top_k], postings_scanned=len(qi), votes=len(rep))
