"""Compare the B2 triplet fingerprint with today's pair fingerprint (D31, D33).

  python -m eval.b2_eval --extra 1000

One catalog for both systems: every val and test positive plus --extra other
indexed fma_medium tracks (seeded). Queries are the val and test splits'
positives and negatives (one excerpt each) under each condition, with the
same seeded excerpts and degradations as eval.run_eval, searched once (no
speed grid) in both indexes in memory. Thresholds are calibrated per system on
val (D13: pooled FAR <= --max-far over all conditions, accuracy objective over
the non-tempo conditions) and applied to test.

Writes eval/results/b2_eval_seed0<tag>.{csv.gz,md,json}.
"""

import argparse
import json
import multiprocessing as mp
import os
from pathlib import Path

import numpy as np
import pandas as pd

from eval.run_eval import condition
from eval.stats import wilson
from eval.summarize import accepted
from prototype import b2, b2_match
from prototype import fingerprint as fp
from prototype import index as pindex
from prototype import match as pmatch
from prototype.audio import DecodeError, decode
from prototype.build_index import machine_spec
from prototype.fma import audio_path

ROOT = Path(__file__).resolve().parent.parent
TEMPO = ("speed_", "keylock_", "stretch_")
CONDITIONS = [
    "clean_10s", "white_snr5_10s", "white_snr0_10s", "mp3_64k_10s",
    "speed_+1pct_10s", "speed_-1pct_10s", "speed_+2pct_10s", "speed_-2pct_10s",
    "speed_+4pct_10s", "speed_-4pct_10s", "speed_+8pct_10s", "speed_-8pct_10s",
    "keylock_+4pct_10s", "keylock_-4pct_10s", "keylock_+8pct_10s", "keylock_-8pct_10s",
]  # fmt: skip

# Set in the parent before the pool forks, so workers share the indexes.
_STATE: dict = {}


def _fingerprint(fma_id: int) -> tuple:
    try:
        pcm = decode(audio_path(fma_id, _STATE["subset"]))
    except DecodeError:
        return fma_id, None, None
    return fma_id, fp.fingerprint(pcm), b2.fingerprint(pcm)


def _query(job: tuple) -> list[dict]:
    split, label, fma_id, seed, conds = job
    try:
        pcm = decode(audio_path(fma_id, _STATE["subset"]))
    except DecodeError:
        return []
    pidx, bidx, cluster, dense = _STATE["pair"], _STATE["b2"], _STATE["cluster"], _STATE["dense"]
    want = dense.get(fma_id, -1)
    rows = []
    for ci, name in enumerate(conds):
        secs, degrade = condition(name, split)
        rng = np.random.default_rng([seed, fma_id, 0, int(secs * 1000)])
        n = int(round(secs * 11025))
        if len(pcm) < n:
            continue
        start = int(rng.integers(0, len(pcm) - n + 1))
        q = degrade(pcm[start : start + n], np.random.default_rng([seed, fma_id, 0, ci]))
        base = {"split": split, "label": label, "fma_id": fma_id, "cond": name}

        def row(system: str, top: int, score: int, runner: int, scale: float, scanned: int,
                base: dict = base) -> dict:  # fmt: skip
            ok = want >= 0 and top >= 0 and cluster[top] == cluster[want]
            return {**base, "system": system, "top1_dense": top, "top1_score": score,
                    "runner_up_score": runner, "scale": scale, "postings_scanned": scanned,
                    "correct": bool(ok)}  # fmt: skip

        h, t = fp.fingerprint_query(q, 4)
        r = pmatch.search(pidx, h, t, top_k=1)
        top = r.candidates[0].track if r.candidates else -1
        score = r.candidates[0].score if r.candidates else 0
        rows.append(row("pairs", top, score, r.runner_up, 1.0, r.postings_scanned))
        rb = b2_match.search(bidx, b2.fingerprint_query(q))
        c = rb.candidates
        top = c[0].track if c else -1
        runner = next((x.score for x in c[1:] if cluster[x.track] != cluster[top]), 0) if c else 0
        score, scale = (c[0].score, c[0].scale) if c else (0, 0.0)
        rows.append(row("b2", top, score, runner, scale, rb.postings_scanned))
    return rows


def calibrate(val: pd.DataFrame, max_far: float) -> tuple[int, float]:
    """D13 grid: pooled FAR over all conditions, accuracy objective over non-tempo ones."""
    neg, pos = val[val["label"] == "neg"], val[val["label"] == "pos"]
    obj = pos[~pos["cond"].str.startswith(TEMPO)]
    best = None
    for ta in range(2, 101):
        for tr in (1.0, 1.25, 1.5, 2.0, 2.5, 3.0, 4.0, 5.0, 6.0, 8.0):
            if accepted(neg, ta, tr).mean() > max_far:
                continue
            acc = (accepted(obj, ta, tr) & obj["correct"].to_numpy()).mean()
            if best is None or acc > best[0]:
                best = (acc, ta, tr)
    if best is None:
        raise SystemExit("no threshold meets the FAR target")
    return best[1], best[2]


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--manifest", default=str(ROOT / "eval/manifests/splits_medium_seed0.json"))
    ap.add_argument("--extra", type=int, default=1000, help="other indexed tracks in the catalog")
    ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--max-far", type=float, default=0.001)
    ap.add_argument("--workers", type=int, default=os.cpu_count())
    ap.add_argument("--tag", default="")
    ap.add_argument(
        "--limit", type=int, default=0, help="first N tracks per split and label (debug)"
    )
    ap.add_argument("--conditions", nargs="+", default=CONDITIONS)
    ap.add_argument("--splits", nargs="+", choices=["val", "test"], default=["val", "test"])
    ap.add_argument(
        "--thresholds-from", type=Path,
        help="an earlier run's JSON: apply its thresholds per system instead of calibrating "
        "on val (new conditions judged by an existing calibration)",
    )  # fmt: skip
    args = ap.parse_args()
    if "val" not in args.splits and args.thresholds_from is None:
        raise SystemExit("without val queries, give --thresholds-from")
    for name in args.conditions:
        condition(name)  # fail fast on a typo, before building the indexes
    m = json.loads(Path(args.manifest).read_text())
    rng = np.random.default_rng(args.seed)
    pos = set(m["val_pos"]) | set(m["test_pos"])
    others = sorted(set(m["indexed"]) - pos)
    ids = sorted(pos | {int(x) for x in rng.choice(others, args.extra, replace=False)})
    _STATE["subset"] = m["meta"]["subset"]
    ctx = mp.get_context("fork")
    with ctx.Pool(args.workers) as pool:
        fps = pool.map(_fingerprint, ids, chunksize=8)
    ok = [(f, p, q) for f, p, q in fps if p is not None]
    ids = [f for f, _, _ in ok]
    clusters = [members for members in m["clusters"].values()]
    pair_index = pindex.build([p for _, p, _ in ok], ids, clusters)
    _STATE["pair"] = pair_index
    _STATE["b2"] = b2_match.build([q for _, _, q in ok])
    _STATE["cluster"] = pair_index.cluster
    _STATE["dense"] = {f: i for i, f in enumerate(ids)}
    print(f"catalog {len(ids)} tracks: pairs {pair_index.n_postings} postings, "
          f"b2 {_STATE['b2'].n_postings}", flush=True)  # fmt: skip
    lim = args.limit or None
    conds = args.conditions
    jobs = [(s, "pos", f, args.seed, conds) for s in args.splits for f in m[f"{s}_pos"][:lim]]
    jobs += [(s, "neg", f, args.seed, conds) for s in args.splits for f in m[f"{s}_neg"][:lim]]
    rows = []
    with ctx.Pool(args.workers) as pool:
        for i, r in enumerate(pool.imap_unordered(_query, jobs, chunksize=4), 1):
            rows += r
            if i % 250 == 0:
                print(f"{i}/{len(jobs)} query tracks", flush=True)
    d = pd.DataFrame(rows)
    out = ROOT / "eval" / "results" / f"b2_eval_seed{args.seed}{args.tag}"
    d.to_csv(out.with_suffix(".csv.gz"), index=False)

    result = {"catalog_tracks": len(ids), "pair_postings": int(pair_index.n_postings),
              "b2_postings": int(_STATE["b2"].n_postings), "systems": {},
              "thresholds_from": args.thresholds_from.name if args.thresholds_from else None,
              "machine": machine_spec()}  # fmt: skip
    fixed = json.loads(args.thresholds_from.read_text()) if args.thresholds_from else None
    source = f"from {args.thresholds_from.name}" if fixed else "calibrated on val"
    lines = [f"# B2 vs pairs, one catalog of {len(ids)} fma_medium tracks", "",
             f"Accuracy at threshold on test (95% Wilson CI); thresholds {source}.", "",
             "| Condition | Pairs | B2 |", "|---|---|---|"]  # fmt: skip
    per = {}
    for system in ("pairs", "b2"):
        ds = d[d["system"] == system]
        if fixed is not None:
            ta, tr = fixed["systems"][system]["t_abs"], fixed["systems"][system]["t_ratio"]
        else:
            ta, tr = calibrate(ds[ds["split"] == "val"], args.max_far)
        test = ds[ds["split"] == "test"]
        neg = test[test["label"] == "neg"]
        far = float(accepted(neg, ta, tr).mean())
        rows_s = {}
        for cond in conds:
            p = test[(test["label"] == "pos") & (test["cond"] == cond)]
            k = int((accepted(p, ta, tr) & p["correct"].to_numpy()).sum())
            rows_s[cond] = {"n": len(p), "accuracy": k / max(len(p), 1), "ci": wilson(k, len(p)),
                            "raw_top1": float(p["correct"].mean()),
                            "median_postings": float(p["postings_scanned"].median())}  # fmt: skip
        result["systems"][system] = {"t_abs": ta, "t_ratio": tr, "test_far": far,
                                     "conditions": rows_s}  # fmt: skip
        per[system] = rows_s
    for cond in conds:
        cells = []
        for system in ("pairs", "b2"):
            r = per[system][cond]
            lo, hi = r["ci"]
            cells.append(f"{100 * r['accuracy']:.1f} ({100 * lo:.1f}-{100 * hi:.1f})")
        lines.append(f"| {cond} | {cells[0]} | {cells[1]} |")
    for system in ("pairs", "b2"):
        s = result["systems"][system]
        med = np.median([r["median_postings"] for r in per[system].values()])
        far = 100 * s["test_far"]
        th = f"t_abs {s['t_abs']}, t_ratio {s['t_ratio']}"
        lines.append(f"\n{system}: {th}, test FAR {far:.2f}%, "
                     f"median postings scanned per query {med:,.0f}")  # fmt: skip
    out.with_suffix(".md").write_text("\n".join(lines) + "\n")
    out.with_suffix(".json").write_text(json.dumps(result, indent=1, default=float) + "\n")
    print("\n".join(lines))


if __name__ == "__main__":
    main()
