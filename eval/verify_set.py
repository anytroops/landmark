"""Check a tracklist against the audio itself, with no human tracklist.

  python -m eval.verify_set --rows data/mine/pitch.rows.csv --entries data/mine/pitch.json \\
      --audio "set.wav" --index data/segments/mine_b2 --out data/mine/pitch.verify.json

For an entry claiming a song at reference seconds = a + rate * mix seconds, the
song's log spectrogram is warped onto the mix by that alignment (with the pitch
moved, or not, whichever fits: turntable or key lock), and every 10 s window is
compared with the mix: each band's level over time, correlated with the mix's
in the same band, and averaged over the bands. Removing each band's mean makes
EQ moves and the other song's steady energy matter little. An entry's score is
the median over its windows. The null is the same statistic with other songs
of the catalog at random positions, the same windows; z is the entry's score in
standard deviations of that null. Independent of the fingerprints: it uses
their alignment to know where to look, not their votes.
"""

import argparse
import json
from collections.abc import Callable
from pathlib import Path

import numpy as np
import pandas as pd

from eval.dj_analysis import FPS, MARGIN, alignment
from prototype import b2
from prototype.audio import decode

WINDOW_S = 10.0
HOP_S = 5.0
N_NULL = 40


def log_spec(pcm: np.ndarray) -> np.ndarray:
    return b2.spectrogram(pcm).astype(np.float32)


def window_corr(mix: np.ndarray, ref: np.ndarray, f0: int, n: int, a: float, rate: float,
                shift: int) -> float:  # fmt: skip
    """Mean per-band correlation of mix frames f0..f0+n with ref warped by
    (a, rate), ref's bands moved up by shift; nan if under 80% of the window
    falls inside ref."""
    frames = np.arange(f0, min(f0 + n, len(mix)))
    idx = np.round((a + rate * frames / FPS) * FPS).astype(np.int64)
    ok = (idx >= 0) & (idx < len(ref))
    if ok.sum() < 0.8 * n:
        return float("nan")
    bands = ref.shape[1]
    m = mix[frames[ok], MARGIN:bands - MARGIN]  # fmt: skip
    r = ref[idx[ok], MARGIN - shift:bands - MARGIN - shift]  # fmt: skip
    m = m - m.mean(0)
    r = r - r.mean(0)
    denom = np.sqrt((m**2).sum(0) * (r**2).sum(0))
    valid = denom > 1e-6
    return float(((m * r).sum(0)[valid] / denom[valid]).mean()) if valid.any() else float("nan")


def starts(start: float, end: float, total: int) -> list[int]:
    """Window starts (frames) across [start, end]: at least one, every HOP_S."""
    n = int(WINDOW_S * FPS)
    first = max(int(start * FPS), 0)
    last = max(first, min(int(end * FPS), total) - n)
    return list(range(first, last + 1, max(int(HOP_S * FPS), 1)))


def score_alignment(mix: np.ndarray, ref: np.ndarray, windows: list[int], a: float,
                    rate: float) -> tuple[float, bool]:  # fmt: skip
    """(median window correlation, pitch moved) at the better of the two
    pitch treatments; nan if no window falls inside ref."""
    n = int(WINDOW_S * FPS)
    best = (float("nan"), False)
    for pitch in (False, True):
        shift = int(round(36 * np.log2(rate))) if pitch else 0
        if abs(shift) > MARGIN or (pitch and shift == 0):
            continue
        c = [window_corr(mix, ref, w, n, a, rate, shift) for w in windows]
        c = [x for x in c if np.isfinite(x)]
        if c and not (np.median(c) <= best[0]):
            best = (float(np.median(c)), pitch)
    return best


def verify_entries(mix: np.ndarray, entries: list[dict], rows: pd.DataFrame,
                   load_ref: Callable[[int], np.ndarray], catalog: list[int],
                   seed: int = 0) -> list[dict]:  # fmt: skip
    """Each entry with its score, null mean and spread, and z."""
    rng = np.random.default_rng(seed)
    out = []
    for e in entries:
        res = {**e, "corr": None, "z": None, "null_mean": None, "null_std": None}
        al = alignment(rows, e["track"], e["start_s"], e["end_s"])
        windows = starts(e["start_s"], e["end_s"], len(mix))
        if al is not None:
            a, rate = al
            corr, pitch = score_alignment(mix, load_ref(e["track"]), windows, a, rate)
            null = []
            others = [t for t in catalog if t != e["track"]]
            while len(null) < N_NULL:
                t = int(rng.choice(others))
                ref = load_ref(t)
                span = len(ref) / FPS
                fake_a = float(rng.uniform(0, max(span - 10, 1))) - e["start_s"]
                c, _ = score_alignment(mix, ref, windows, fake_a, 1.0)
                if np.isfinite(c):
                    null.append(c)
            mean, std = float(np.mean(null)), float(np.std(null))
            res.update(
                corr=corr,
                pitch=pitch,
                a=a,
                rate=rate,
                null_mean=mean,
                null_std=std,
                z=(corr - mean) / std if np.isfinite(corr) and std > 0 else None,
            )
        out.append(res)
    return out


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--rows", type=Path, required=True)
    ap.add_argument("--entries", type=Path, required=True)
    ap.add_argument("--audio", type=Path, required=True)
    ap.add_argument("--index", type=Path, required=True)
    ap.add_argument("--out", type=Path, required=True)
    args = ap.parse_args()
    rows = pd.read_csv(args.rows)
    entries = json.loads(args.entries.read_text())
    paths = {
        int(x["dense"]): x["path"] for x in json.loads((args.index / "extras.json").read_text())
    }
    cache: dict[int, np.ndarray] = {}

    def load_ref(t: int) -> np.ndarray:
        if t not in cache:
            cache[t] = log_spec(decode(Path(paths[t])))
        return cache[t]

    mix = log_spec(decode(args.audio))
    pool = np.random.default_rng(1).choice(sorted(paths), size=min(60, len(paths)), replace=False)
    res = verify_entries(mix, entries, rows, load_ref, [int(x) for x in pool])
    args.out.write_text(json.dumps(res, indent=1, default=float) + "\n")
    for r in res:
        z = "-" if r["z"] is None else f"{r['z']:6.1f}"
        c = "-" if r["corr"] is None else f"{r['corr']:.3f}"
        print(f"{r['start']:>6} ev {r['evidence']:7.0f} corr {c:>6} z {z:>6}  {r['name'][:56]}")


if __name__ == "__main__":
    main()
