"""Fingerprint every indexed track in a split manifest and write the index.

Decode failures are logged, counted, and skipped; they never stop the build.
Stats go to eval/results/ (committed); the index itself goes to data/ (ignored).
"""

import argparse
import json
import logging
import os
import platform
import subprocess
import time
from multiprocessing import Pool
from pathlib import Path

import numpy as np

from prototype import fingerprint as fp
from prototype.audio import SAMPLE_RATE, DecodeError, decode
from prototype.fma import DATA_DIR, audio_path
from prototype.index import build

ROOT = Path(__file__).resolve().parent.parent
log = logging.getLogger("build_index")


def _work(args: tuple[int, str]) -> tuple[int, np.ndarray | None, np.ndarray | None, float, str]:
    fma_id, subset = args
    try:
        pcm = decode(audio_path(fma_id, subset))
    except DecodeError as e:
        return fma_id, None, None, 0.0, str(e)
    h, t = fp.fingerprint(pcm)
    return fma_id, h, t, len(pcm) / SAMPLE_RATE, ""


def fingerprint_tracks(
    ids: list[int], subset: str, workers: int | None
) -> tuple[dict[int, tuple[np.ndarray, np.ndarray, float]], list[dict[str, object]]]:
    """Fingerprint tracks in parallel. Returns ({fma_id: (hashes, t1, seconds)}, failures)."""
    results: dict[int, tuple[np.ndarray, np.ndarray, float]] = {}
    failures: list[dict[str, object]] = []
    with Pool(workers) as pool:
        for i, (fid, h, t, dur, err) in enumerate(
            pool.imap_unordered(_work, [(f, subset) for f in ids], chunksize=16), 1
        ):
            if h is None:
                log.warning("decode failed: %s", err)
                failures.append({"fma_id": fid, "error": err})
            else:
                results[fid] = (h, t, dur)
            if i % 1000 == 0:
                log.info("%d/%d fingerprinted", i, len(ids))
    return results, failures


def machine_spec() -> dict[str, object]:
    def sh(cmd: list[str]) -> str:
        try:
            return subprocess.run(cmd, capture_output=True, text=True, check=True).stdout.strip()
        except (OSError, subprocess.CalledProcessError):
            return "unknown"

    return {
        "platform": platform.platform(),
        "cpu": sh(["sysctl", "-n", "machdep.cpu.brand_string"]),
        "cores": os.cpu_count(),
        "python": platform.python_version(),
        "commit": sh(["git", "-C", str(ROOT), "rev-parse", "--short", "HEAD"]),
        # Code or inputs changed since the commit. Result folders are outputs that
        # earlier pipeline steps rewrite, so they are excluded.
        "dirty": sh(
            [
                "git",
                "-C",
                str(ROOT),
                "status",
                "--porcelain",
                "--untracked-files=no",
                "--",
                ".",
                ":(exclude)eval/results",
                ":(exclude)bench/results",
            ]
        )
        != "",
    }


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument(
        "--manifest", type=Path, default=ROOT / "eval/manifests/splits_small_seed0.json"
    )
    ap.add_argument("--workers", type=int, default=os.cpu_count())
    ap.add_argument("--limit", type=int, default=0, help="index only the first N tracks (debug)")
    args = ap.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")

    manifest = json.loads(args.manifest.read_text())
    subset = manifest["meta"]["subset"]
    ids = manifest["indexed"][: args.limit or None]

    t0 = time.perf_counter()
    results, failures = fingerprint_tracks(ids, subset, args.workers)
    fp_secs = time.perf_counter() - t0

    ok_ids = sorted(results)
    index = build([results[f][:2] for f in ok_ids], ok_ids, list(manifest["clusters"].values()))
    out = DATA_DIR / "index" / f"{args.manifest.stem}.npz"
    out.parent.mkdir(parents=True, exist_ok=True)
    index.save(out)
    total_secs = time.perf_counter() - t0

    audio_secs = sum(r[2] for r in results.values())
    key_sizes = np.diff(index.offsets)
    stats = {
        "manifest": args.manifest.name,
        "params": fp.DEFAULT.__dict__,
        "tracks_requested": len(ids),
        "tracks_indexed": index.n_tracks,
        "decode_failures": len(failures),
        "failures": failures,
        "audio_seconds": round(audio_secs, 1),
        "n_keys": len(index.keys),
        "n_postings": index.n_postings,
        "landmarks_per_audio_second": round(index.n_postings / audio_secs, 2),
        "postings_per_key": {
            "mean": round(float(key_sizes.mean()), 2),
            "p50": int(np.percentile(key_sizes, 50)),
            "p99": int(np.percentile(key_sizes, 99)),
            "max": int(key_sizes.max()),
        },
        "raw_bytes_per_posting": 8,
        "workers": args.workers,
        "fingerprint_wall_seconds": round(fp_secs, 1),
        "total_wall_seconds": round(total_secs, 1),
        "machine": machine_spec(),
    }
    res_dir = ROOT / "eval" / "results"
    res_dir.mkdir(exist_ok=True)
    stats_path = res_dir / f"index_stats_{args.manifest.stem}.json"
    stats_path.write_text(json.dumps(stats, indent=2) + "\n")
    log.info("wrote %s and %s", out, stats_path)


if __name__ == "__main__":
    main()
