"""Run identification queries for one split and write per-query rows.

Each query track is decoded once; every condition reuses the same seeded
excerpt start (per length), so conditions are paired comparisons.
Positives: one excerpt per {split}_pos track. Negatives: NEG_REPS excerpts per
{split}_neg track (never indexed).

Output: eval/results/queries_{split}_seed{seed}.csv.gz (committed).

With --stop-fractions, each query is fingerprinted once and searched once
per cutoff, dropping the landmarks the coordinator's stop list would drop
(D30); each cutoff above 0 writes its own file, tagged _stop{fraction}.
"""

import argparse
import json
import logging
import os
import re
import time
from collections.abc import Callable
from multiprocessing import Pool
from pathlib import Path

import numpy as np
import pandas as pd

from eval.backends import LocalBackend, ShardBackend, dense_tables
from eval.degrade import add_noise, excerpt, keylock, reencode, speed, stretch
from prototype import fingerprint as fp
from prototype.audio import DecodeError, decode
from prototype.build_index import machine_spec
from prototype.fma import DATA_DIR, audio_path
from prototype.stoplist import StopList

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


# Any tempo change by name: speed_{+x}pct_{n}s is turntable style (tempo and
# pitch together); keylock_{+x}pct_{n}s keeps pitch by WSOLA, as DJ key lock
# does; stretch_{+x}pct_{n}s keeps pitch with librosa's phase vocoder, a poor
# model of key lock kept for the results that used it (degrade.stretch).
_SCALED = re.compile(r"(speed|keylock|stretch)_([+-]\d+(?:\.\d+)?)pct_(\d+)s")
_TEMPO = {"speed": speed, "keylock": keylock, "stretch": stretch}


def condition(name: str) -> tuple[float, Degrade]:
    """(excerpt seconds, degradation) for a named condition."""
    if name in CONDITIONS:
        return CONDITIONS[name]
    m = _SCALED.fullmatch(name)
    if m is None:
        raise KeyError(f"unknown condition {name!r}")
    fn = _TEMPO[m.group(1)]
    factor = 1 + float(m.group(2)) / 100
    return float(m.group(3)), lambda x, r: fn(x, factor)


def _work(job: tuple[int, str, int, int, str, list[str], int]) -> list[dict[str, object]]:
    fma_id, label, rep, seed, subset, conds, shifts = job
    try:
        pcm = decode(audio_path(fma_id, subset))
    except DecodeError as e:
        return [{"fma_id": fma_id, "label": label, "rep": rep, "error": str(e)}]
    rows = []
    for ci, name in enumerate(conds):
        secs, degrade = condition(name)
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
    ap.add_argument(
        "--stop-fractions", type=float, nargs="+", default=[0.0],
        help="stop list cutoffs to evaluate, as fractions of catalog tracks (0 = off, D30)",
    )  # fmt: skip
    ap.add_argument("--stoplist", type=Path, help="stoplist.lmst of the searched index")
    ap.add_argument("--positives-only", action="store_true",
                    help="skip never-indexed queries (for curves that need no FAR)")  # fmt: skip
    args = ap.parse_args()
    for name in args.conditions:
        condition(name)  # fail fast on a typo, before any decoding
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
    stop = StopList.load(args.stoplist) if args.stoplist else None
    if any(f > 0 for f in args.stop_fractions) and stop is None:
        raise SystemExit("--stop-fractions above 0 need --stoplist")
    if stop is not None and stop.n_tracks != len(dense_to_fma):
        raise SystemExit(
            f"{args.stoplist} covers {stop.n_tracks} tracks, the index has {len(dense_to_fma)}"
        )
    # fraction -> largest posting count kept, or None for no stop list
    limits = {f: (stop.limit(f) if f > 0 else None) for f in args.stop_fractions}
    lim = args.limit or None
    jobs = [
        (f, "pos", 0, args.seed, subset, args.conditions, args.query_shifts)
        for f in manifest[f"{args.split}_pos"][:lim]
    ]
    if not args.positives_only:
        jobs += [
            (f, "neg", r, args.seed, subset, args.conditions, args.query_shifts)
            for f in manifest[f"{args.split}_neg"][:lim]
            for r in range(NEG_REPS)
        ]

    out_rows: dict[float, list[dict[str, object]]] = {f: [] for f in limits}
    search_secs = dict.fromkeys(limits, 0.0)
    t0 = time.perf_counter()
    with Pool(args.workers) as pool:
        for i, rows in enumerate(pool.imap_unordered(_work, jobs, chunksize=4), 1):
            for r in rows:
                if "error" in r:
                    log.warning("skip %s: %s", r["fma_id"], r["error"])
                    for f in limits:
                        out_rows[f].append(dict(r))
                    continue
                hashes, times = r.pop("hashes"), r.pop("times")
                for f, limit in limits.items():
                    h, t = hashes, times
                    if limit is not None:
                        keep = stop.keep(hashes, limit)
                        h, t = hashes[keep], times[keep]
                    s0 = time.perf_counter()
                    hit = backend.search(h, t)
                    search_secs[f] += time.perf_counter() - s0
                    top1 = int(dense_to_fma[hit.top1_dense]) if hit.top1_dense >= 0 else -1
                    row = dict(r)
                    row.update(
                        n_landmarks_searched=len(h),
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
                    out_rows[f].append(row)
            if i % 200 == 0:
                log.info("%d/%d tracks", i, len(jobs))

    for f, limit in limits.items():
        tag = args.tag + (f"_stop{f:g}" if f > 0 else "")
        df = pd.DataFrame(out_rows[f])
        out = ROOT / "eval" / "results" / f"queries_{args.split}_seed{args.seed}{tag}.csv.gz"
        df.sort_values(["label", "fma_id", "rep", "cond"]).to_csv(out, index=False)
        meta = {
            "split": args.split, "seed": args.seed, "manifest": args.manifest.name,
            "conditions": args.conditions, "limit": args.limit,
            "query_shifts": args.query_shifts, "tag": tag,
            "positives_only": args.positives_only,
            "stop_fraction": f, "stop_limit": limit,
            "stoplist": (f"{args.stoplist} crc32c {stop.crc:08x}" if stop is not None else None),
            "backend": f"shard {args.shard}" if args.shard else "python",
            "backend_retries": getattr(backend, "retries", 0), "rows": len(df),
            "wall_seconds": round(time.perf_counter() - t0, 1),
            "mean_search_ms": round(1000 * search_secs[f] / max(len(df), 1), 3),
            "machine": machine_spec(),
        }  # fmt: skip
        out.with_name(f"queries_{args.split}_seed{args.seed}{tag}.meta.json").write_text(
            json.dumps(meta, indent=2) + "\n"
        )
        log.info(
            "wrote %s: %d rows, %.3f ms mean search",
            out, len(df), 1000 * search_secs[f] / max(len(df), 1),
        )  # fmt: skip


if __name__ == "__main__":
    main()
