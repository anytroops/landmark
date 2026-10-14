"""Run identification queries for one split and write per-query rows.

Each query track is decoded once; every condition reuses the same seeded
excerpt start (per length), so conditions are paired comparisons.
Positives: one excerpt per {split}_pos track. Negatives: NEG_REPS excerpts per
{split}_neg track (never indexed).

Output: eval/results/queries_{split}_seed{seed}.csv.gz (committed).
"""

import argparse
import json
import logging
import os
import time
from collections.abc import Callable
from multiprocessing import Pool
from pathlib import Path

import numpy as np
import pandas as pd

from eval.backends import LocalBackend, ShardBackend, dense_tables
from eval.degrade import add_noise, excerpt, reencode, speed
from prototype import fingerprint as fp
from prototype.audio import DecodeError, decode
from prototype.build_index import machine_spec
from prototype.fma import DATA_DIR, audio_path

ROOT = Path(__file__).resolve().parent.parent
NEG_REPS = 2
log = logging.getLogger("run_eval")

Degrade = Callable[[np.ndarray, np.random.Generator], np.ndarray]

# name -> (excerpt seconds, degradation)
CONDITIONS: dict[str, tuple[float, Degrade]] = {
    "clean_10s": (10.0, lambda x, r: x),
    "clean_5s": (5.0, lambda x, r: x),
    "white_snr15_10s": (10.0, lambda x, r: add_noise(x, 15, r)),
    "white_snr10_10s": (10.0, lambda x, r: add_noise(x, 10, r)),
    "white_snr5_10s": (10.0, lambda x, r: add_noise(x, 5, r)),
    "white_snr0_10s": (10.0, lambda x, r: add_noise(x, 0, r)),
    "white_snr5_5s": (5.0, lambda x, r: add_noise(x, 5, r)),
    "mp3_128k_10s": (10.0, lambda x, r: reencode(x, "128k")),
    "mp3_64k_10s": (10.0, lambda x, r: reencode(x, "64k")),
    "speed_+2pct_10s": (10.0, lambda x, r: speed(x, 1.02)),
    "speed_-2pct_10s": (10.0, lambda x, r: speed(x, 0.98)),
}


def _work(job: tuple[int, str, int, int, str, list[str], int]) -> list[dict[str, object]]:
    fma_id, label, rep, seed, subset, conds, shifts = job
    try:
        pcm = decode(audio_path(fma_id, subset))
    except DecodeError as e:
        return [{"fma_id": fma_id, "label": label, "rep": rep, "error": str(e)}]
    rows = []
    for ci, name in enumerate(conds):
        secs, degrade = CONDITIONS[name]
        start_rng = np.random.default_rng([seed, fma_id, rep, int(secs * 1000)])
        try:
            clip, start = excerpt(pcm, secs, start_rng)
        except ValueError as e:
            rows.append(
                {"fma_id": fma_id, "label": label, "rep": rep, "cond": name, "error": str(e)}
            )
            continue
        q = degrade(clip, np.random.default_rng([seed, fma_id, rep, ci]))
        h, t = fp.fingerprint_query(q, shifts)
        rows.append(
            {"fma_id": fma_id, "label": label, "rep": rep, "cond": name, "start": start,
             "n_landmarks": len(h), "hashes": h, "times": t}
        )  # fmt: skip
    return rows


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--split", choices=["val", "test"], required=True)
    ap.add_argument(
        "--manifest", type=Path, default=ROOT / "eval/manifests/splits_small_seed0.json"
    )
    ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--workers", type=int, default=os.cpu_count())
    ap.add_argument("--conditions", nargs="*", default=list(CONDITIONS))
    ap.add_argument("--query-shifts", type=int, default=4, help="sub-hop query grids (D15)")
    ap.add_argument("--tag", default="", help="suffix for result file names, e.g. _shift4")
    ap.add_argument("--limit", type=int, default=0, help="first N tracks per label (debug)")
    ap.add_argument(
        "--shard", default="", help="host:port of a cmd/shard to query instead of the Python index"
    )
    args = ap.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")

    manifest = json.loads(args.manifest.read_text())
    subset = manifest["meta"]["subset"]
    dense_to_fma, dense_cluster = dense_tables(manifest)
    if args.shard:
        backend = ShardBackend(args.shard, dense_cluster)
    else:
        backend = LocalBackend(DATA_DIR / "index" / f"{args.manifest.stem}.npz")
        assert np.array_equal(backend.index.fma_ids, dense_to_fma), "index and manifest disagree"
    cluster = {t: int(k) for k, members in manifest["clusters"].items() for t in members}
    lim = args.limit or None
    jobs = [
        (f, "pos", 0, args.seed, subset, args.conditions, args.query_shifts)
        for f in manifest[f"{args.split}_pos"][:lim]
    ]
    jobs += [
        (f, "neg", r, args.seed, subset, args.conditions, args.query_shifts)
        for f in manifest[f"{args.split}_neg"][:lim]
        for r in range(NEG_REPS)
    ]

    out_rows: list[dict[str, object]] = []
    t0 = time.perf_counter()
    search_secs = 0.0
    with Pool(args.workers) as pool:
        for i, rows in enumerate(pool.imap_unordered(_work, jobs, chunksize=4), 1):
            for r in rows:
                if "error" in r:
                    log.warning("skip %s: %s", r["fma_id"], r["error"])
                    out_rows.append(r)
                    continue
                s0 = time.perf_counter()
                hit = backend.search(r.pop("hashes"), r.pop("times"))
                search_secs += time.perf_counter() - s0
                top1 = int(dense_to_fma[hit.top1_dense]) if hit.top1_dense >= 0 else -1
                r.update(
                    top1_fma=top1,
                    top1_score=hit.top1_score,
                    runner_up_score=hit.runner_up,
                    top1_offset=hit.top1_offset,
                    postings_scanned=hit.postings_scanned,
                    correct=bool(
                        r["label"] == "pos"
                        and cluster.get(top1, top1) == cluster.get(r["fma_id"], r["fma_id"])
                    ),
                )
                out_rows.append(r)
            if i % 200 == 0:
                log.info("%d/%d tracks", i, len(jobs))

    df = pd.DataFrame(out_rows)
    out = ROOT / "eval" / "results" / f"queries_{args.split}_seed{args.seed}{args.tag}.csv.gz"
    df.sort_values(["label", "fma_id", "rep", "cond"]).to_csv(out, index=False)
    meta = {
        "split": args.split, "seed": args.seed, "manifest": args.manifest.name,
        "conditions": args.conditions, "limit": args.limit,
        "query_shifts": args.query_shifts, "tag": args.tag,
        "backend": f"shard {args.shard}" if args.shard else "python", "rows": len(df),
        "wall_seconds": round(time.perf_counter() - t0, 1),
        "mean_search_ms": round(1000 * search_secs / max(len(df), 1), 3),
        "machine": machine_spec(),
    }  # fmt: skip
    out.with_name(f"queries_{args.split}_seed{args.seed}{args.tag}.meta.json").write_text(
        json.dumps(meta, indent=2) + "\n"
    )
    log.info(
        "wrote %s: %d rows, %.1fs wall, %.3f ms mean search",
        out, len(df), time.perf_counter() - t0, 1000 * search_secs / max(len(df), 1),
    )  # fmt: skip


if __name__ == "__main__":
    main()
