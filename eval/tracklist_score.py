"""Turn window matches into tracklists and score them (D32).

  python -m eval.tracklist_score --mixes val_mixed8 --calibrate      # pick thresholds
  python -m eval.tracklist_score --mixes test_mixed8 --params <val result json>

Input: eval/results/tracklist/windows_<mixes><tag>.csv.gz from eval.tracklist
(one row per window x speed hypothesis) and the ground truth eval/mixes/<mixes>.json.

Decoders:
- "smooth" (D32 option i, the baseline): per window, take the best-scoring
  hypothesis; accept it if score >= t_abs and score >= t_ratio * runner-up;
  majority-vote each window with its neighbors; runs of one track become
  entries.
- "align" (D32 options ii + iii): for every candidate track, fit a line
  through its matches in (mix time, reference time); a real play advances
  linearly at a steady speed and chance matches do not, so only matches near
  the line count, and the slope estimates the speed. Then Viterbi over the
  windows picks one track (or "unknown") per window, rewarding a window's
  on-line evidence for its track and charging a penalty per switch.

Scoring, per mix, against known (indexed) segments, duplicates counted as
the same track (D12):
- an entry is correct if a known segment of the same track overlaps it;
  precision = correct entries / entries, recall = known segments found /
  known segments (an entry on a never-indexed segment is a false positive);
- start error: |entry start - true transition|, where a transition is the
  middle of the crossfade into the segment (the mix start for the first);
- speed error: |estimated - true| tempo change, in percentage points.
"""

import argparse
import json
from dataclasses import dataclass
from pathlib import Path

import numpy as np
import pandas as pd

from eval.tracklist import cluster_table

ROOT = Path(__file__).resolve().parent.parent
RES = ROOT / "eval" / "results" / "tracklist"
FRAMES_PER_S = 11025 / 256


@dataclass
class Entry:
    track: int  # dense id
    start: float  # seconds in the mix
    end: float
    factor: float  # estimated tempo factor (1 = unchanged)
    evidence: float


def best_per_window(w: pd.DataFrame) -> pd.DataFrame:
    """The highest-scoring hypothesis of each window."""
    i = w.groupby(["mix", "t0"])["score"].idxmax()
    return w.loc[i].sort_values(["mix", "t0"]).reset_index(drop=True)


def accepted(df: pd.DataFrame, t_abs: float, t_ratio: float) -> np.ndarray:
    s, r = df["score"].to_numpy(), df["runner_up"].clip(lower=1).to_numpy()
    return (df["top1_dense"].to_numpy() >= 0) & (s >= t_abs) & (s >= t_ratio * r)


def runs(labels: list[int], t0s: list[float], window: float) -> list[tuple[int, float, float]]:
    """Contiguous runs of the same non-negative label as (label, start, end)."""
    out = []
    for i, lab in enumerate(labels):
        if lab < 0:
            continue
        if out and out[-1][0] == lab and labels[i - 1] == lab:
            out[-1] = (lab, out[-1][1], t0s[i] + window)
        else:
            out.append((lab, t0s[i], t0s[i] + window))
    return out


def decode_smooth(w: pd.DataFrame, window: float, t_abs: float, t_ratio: float,
                  k: int = 1) -> list[Entry]:  # fmt: skip
    """Option i: accepted best hypothesis per window, majority over +/-k windows."""
    best = best_per_window(w)
    lab = np.where(accepted(best, t_abs, t_ratio), best["top1_dense"], -1)
    smoothed = []
    for i in range(len(lab)):
        vals, counts = np.unique(lab[max(0, i - k) : i + k + 1], return_counts=True)
        smoothed.append(int(vals[np.argmax(counts)]))
    t0s = best["t0"].tolist()
    entries = []
    for track, a, b in runs(smoothed, t0s, window):
        rows = best[(best["t0"] >= a) & (best["t0"] + window <= b) & (best["top1_dense"] == track)]
        f = float(np.median(rows["factor"])) if len(rows) else 1.0
        entries.append(Entry(track, a, b, f, float(rows["score"].sum())))
    return entries


def fit_line(t: np.ndarray, r: np.ndarray, w: np.ndarray, tol: float,
             slopes: np.ndarray) -> tuple[float, float, np.ndarray]:  # fmt: skip
    """Robust line r = intercept + slope * t. A search over candidate slopes
    and intercepts finds the set of points with the most weight within
    +/-tol seconds of some line (many nearby slopes tie on that set over a
    short span), then weighted least squares on that set gives the slope and
    intercept, and the inliers are recomputed against it. Returns (slope,
    intercept, inlier mask)."""
    best_weight, best = 0.0, np.zeros(len(t), bool)
    for s in slopes:
        a = r - s * t
        for a0 in a:
            inl = np.abs(a - a0) <= tol
            weight = float(w[inl].sum())
            if weight > best_weight:
                best_weight, best = weight, inl
    if best.sum() >= 2 and np.ptp(t[best]) > 0:
        slope, intercept = np.polyfit(t[best], r[best], 1, w=np.sqrt(w[best]))
    else:
        slope, intercept = 1.0, float(np.median(r[best] - t[best])) if best.any() else 0.0
    inl = np.abs(r - (intercept + slope * t)) <= tol
    return float(slope), float(intercept), inl


def decode_align(w: pd.DataFrame, window: float, min_evidence: float, switch: float,
                 tol: float = 0.3, min_windows: int = 2) -> list[Entry]:  # fmt: skip
    """Options ii + iii: per-track line fits, then Viterbi with a switch penalty."""
    t0s = sorted(w["t0"].unique())
    idx = {t: i for i, t in enumerate(t0s)}
    # For each window and track, keep that track's best hypothesis.
    cand = w[w["top1_dense"] >= 0]
    cand = cand.loc[cand.groupby(["t0", "top1_dense"])["score"].idxmax()]
    slopes = np.round(np.arange(0.90, 1.1001, 0.0025), 4)
    tracks: dict[int, tuple[float, float, dict[int, float]]] = {}
    for track, g in cand.groupby("top1_dense"):
        if len(g) < min_windows:
            continue
        t = g["t0"].to_numpy(dtype=float)
        r = g["offset"].to_numpy(dtype=float) / FRAMES_PER_S  # reference time at window start
        s, a, inl = fit_line(t, r, g["score"].to_numpy(dtype=float), tol, slopes)
        on = g[inl]
        if len(on) < min_windows or on["score"].sum() < min_evidence:
            continue
        tracks[int(track)] = (
            s,
            a,
            {idx[x]: float(sc) for x, sc in zip(on["t0"], on["score"], strict=True)},
        )
    if not tracks:
        return []
    states = [-1, *tracks]  # -1 = unknown
    n, m = len(t0s), len(states)
    emit = np.zeros((n, m))
    for j, track in enumerate(states[1:], 1):
        for i, sc in tracks[track][2].items():
            emit[i, j] = np.log1p(sc)
    emit[:, 0] = np.log1p(min_evidence / min_windows)  # what a window must beat to leave "unknown"
    # Viterbi: score[i, j] = emit[i, j] + max(score[i-1, j], max_k score[i-1, k] - switch)
    score = np.zeros((n, m))
    back = np.zeros((n, m), dtype=int)
    score[0] = emit[0]
    for i in range(1, n):
        prev = score[i - 1]
        k = int(np.argmax(prev))
        stay = prev
        move = prev[k] - switch
        choose_move = move > stay
        score[i] = emit[i] + np.where(choose_move, move, stay)
        back[i] = np.where(choose_move, k, np.arange(m))
    path = [int(np.argmax(score[-1]))]
    for i in range(n - 1, 0, -1):
        path.append(int(back[i, path[-1]]))
    labels = [states[j] for j in reversed(path)]
    entries = []
    for track, a, b in runs(labels, t0s, window):
        s, _, ev = tracks[track]
        entries.append(Entry(track, a, b, s, float(sum(ev.values()))))
    return entries


def merge_repeats(entries: list[Entry], gap: float) -> list[Entry]:
    """Join consecutive entries of the same track that overlap or sit within
    gap seconds (a decoder that drops to unknown for a window and comes back
    splits one play); the merged entry keeps the larger evidence and its
    factor. As internal/tracklist.MergeRepeats, which cmd/tracklist applies."""
    out: list[Entry] = []
    for e in entries:
        if out and out[-1].track == e.track and e.start <= out[-1].end + gap:
            last = out[-1]
            last.end = max(last.end, e.end)
            if e.evidence > last.evidence:
                last.evidence, last.factor = e.evidence, e.factor
            continue
        out.append(Entry(e.track, e.start, e.end, e.factor, e.evidence))
    return out


def score_mix(entries: list[Entry], segments: list[dict], cluster: np.ndarray) -> dict:
    """Match entries to known segments; returns counts and errors."""

    def same(a: int, b: int) -> bool:
        return a >= 0 and b >= 0 and cluster[a] == cluster[b]

    # Ignored segments (e.g. cut off by a truncated file) neither count toward
    # recall nor make an entry that names them a false positive.
    known = [s for s in segments if s["known"]]
    scored = [i for i, s in enumerate(known) if not s.get("ignore")]
    found = set()
    correct = repeats = 0
    start_err, speed_err = [], []
    for e in entries:
        hits = [
            i
            for i, s in enumerate(known)
            if same(e.track, s["dense"]) and e.start < s["mix_end"] and e.end > s["mix_start"]
        ]
        if not hits:
            continue
        correct += 1
        i = hits[0]
        if i in found or known[i].get("ignore"):
            repeats += i in found  # a second entry for a segment already found
            continue  # correct, but adds no recall (repeat entry, or an ignored segment)
        found.add(i)
        s = known[i]
        transition = s["mix_start"] + s["fade_in"] / 2
        start_err.append(abs(e.start - transition))
        speed_err.append(abs((e.factor - 1) * 100 - s["pct"]))
    return {"entries": len(entries), "correct": correct, "known": len(scored),
            "found": len(found), "repeats": repeats, "start_err": start_err,
            "speed_err": speed_err}  # fmt: skip


def evaluate(w: pd.DataFrame, gt: dict, cluster: np.ndarray, decoder: str, params: dict,
             window: float, merge_gap: float = -1) -> dict:  # fmt: skip
    totals = {"entries": 0, "correct": 0, "known": 0, "found": 0, "repeats": 0}
    start_err, speed_err = [], []
    for k, mix in enumerate(gt["mixes"]):
        wk = w[w["mix"] == k]
        if decoder == "smooth":
            entries = decode_smooth(
                wk, window, params["t_abs"], params["t_ratio"], params.get("k", 1)
            )
        else:
            entries = decode_align(wk, window, params["min_evidence"], params["switch"])
        if merge_gap >= 0:
            entries = merge_repeats(entries, merge_gap)
        r = score_mix(entries, mix["segments"], cluster)
        for key in totals:
            totals[key] += r[key]
        start_err += r["start_err"]
        speed_err += r["speed_err"]
    p = totals["correct"] / max(totals["entries"], 1)
    rc = totals["found"] / max(totals["known"], 1)
    se = np.array(start_err) if start_err else np.array([np.nan])
    return {
        "decoder": decoder, "params": params, **totals,
        "precision": p, "recall": rc, "f1": 2 * p * rc / max(p + rc, 1e-9),
        "start_within_5s": float(np.mean(se <= 5)), "start_within_10s": float(np.mean(se <= 10)),
        "start_within_20s": float(np.mean(se <= 20)), "start_err_median_s": float(np.nanmedian(se)),
        "speed_err_median_pct": float(np.median(speed_err)) if speed_err else float("nan"),
    }  # fmt: skip


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--mixes", required=True)
    ap.add_argument("--tag", default="")
    ap.add_argument("--manifest", default=str(ROOT / "eval/manifests/splits_medium_seed0.json"))
    ap.add_argument("--catalog", help="catalog.json of the searched index (default: manifest)")
    ap.add_argument("--calibrate", action="store_true", help="grid-search decoder parameters (val)")
    ap.add_argument("--params", type=Path, help="calibration result JSON to apply (test)")
    ap.add_argument("--group-by", help="also score subsets by a mix field, e.g. timescale")
    ap.add_argument("--sets", nargs="+", help="only mixes whose 'set' field is one of these")
    ap.add_argument(
        "--merge-gap",
        type=float,
        default=-1,
        help="join consecutive entries of one track within this many seconds, as cmd/tracklist "
        "does (default off); results get a _merged suffix",
    )
    ap.add_argument(
        "--identity-only",
        action="store_true",
        help="use only the window as is: today's system without the speed grid, or B2 "
        "without the double- and half-time hypotheses",
    )
    args = ap.parse_args()

    gt = json.loads((ROOT / "eval" / "mixes" / f"{args.mixes}.json").read_text())
    stem = f"windows_{args.mixes}{args.tag}"
    w = pd.read_csv(RES / f"{stem}.csv.gz")
    if args.sets:  # keep only the chosen sets, renumbering mixes as the window rows do
        idx = [i for i, m in enumerate(gt["mixes"]) if m.get("set") in args.sets]
        gt = {**gt, "mixes": [gt["mixes"][i] for i in idx]}
        w = w[w["mix"].isin(idx)].copy()
        w["mix"] = w["mix"].map({old: new for new, old in enumerate(idx)})
    suffix = "_" + "-".join(args.sets) if args.sets else ""
    if args.identity_only:
        w, suffix = w[w["mode"].isin(["none", "b2"])], suffix + "_identity"
    if args.merge_gap >= 0:
        suffix += "_merged"
    window = json.loads((RES / f"{stem}.meta.json").read_text())["window_s"]
    cluster = cluster_table(args.manifest, args.catalog)

    if args.calibrate:
        grids = {
            "smooth": [{"t_abs": a, "t_ratio": r, "k": k} for a in (10, 20, 40, 80, 160)
                       for r in (1.5, 2, 3, 4) for k in (0, 1, 2)],
            "align": [{"min_evidence": e, "switch": s} for e in (50, 100, 200, 400, 800)
                      for s in (1, 2, 4, 8, 16)],
        }  # fmt: skip
        out = {}
        for decoder, grid in grids.items():
            results = [evaluate(w, gt, cluster, decoder, p, window, args.merge_gap) for p in grid]
            out[decoder] = max(results, key=lambda r: r["f1"])
            print(decoder, json.dumps({k: v for k, v in out[decoder].items()}, default=float))
        path = RES / f"calibration_{args.mixes}{args.tag}{suffix}.json"
    else:
        params = json.loads(args.params.read_text())
        out = {
            d: evaluate(w, gt, cluster, d, params[d]["params"], window, args.merge_gap)
            for d in ("smooth", "align")
        }
        for d, r in out.items():
            print(d, json.dumps(r, default=float))
        if args.group_by:
            groups = {}
            for value in sorted({m[args.group_by] for m in gt["mixes"]}):
                idx = [i for i, m in enumerate(gt["mixes"]) if m[args.group_by] == value]
                sub_gt = {**gt, "mixes": [gt["mixes"][i] for i in idx]}
                sub_w = w[w["mix"].isin(idx)].copy()
                sub_w["mix"] = sub_w["mix"].map({old: new for new, old in enumerate(idx)})
                groups[value] = {
                    d: evaluate(
                        sub_w, sub_gt, cluster, d, params[d]["params"], window, args.merge_gap
                    )
                    for d in ("smooth", "align")
                }
                for d, r in groups[value].items():
                    print(
                        value, d, f"found {r['found']}/{r['known']} precision {r['precision']:.3f}"
                    )
            out["by_" + args.group_by] = groups
        path = RES / f"score_{args.mixes}{args.tag}{suffix}.json"
    path.write_text(json.dumps(out, indent=1, default=float) + "\n")
    print(path)


if __name__ == "__main__":
    main()
