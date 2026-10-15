"""B5: ingest throughput (decode + fingerprint + segment write) vs worker count.

  python -m bench.scripts.b5_ingest --tracks 600 --workers 1 2 4 8 12 --out bench/results/b5_small

Uses the first --tracks indexed tracks of the manifest, one bin/ingest run
per worker count (P=1). Throughput counts the fingerprint phase (decode via
ffmpeg plus fingerprinting, the part workers parallelize) separately from
segment writing.
"""

import argparse
import json
import os
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def require_clean_tree() -> None:
    """Refuse to benchmark uncommitted code: results must trace to a commit."""
    out = subprocess.run(
        ["git", "-C", str(ROOT), "status", "--porcelain", "--untracked-files=no", "--", ".",
         ":(exclude)eval/results", ":(exclude)bench/results", ":(exclude)docs"],
        capture_output=True, text=True, check=True,
    ).stdout  # fmt: skip
    if out.strip() and not os.environ.get("LANDMARK_ALLOW_DIRTY"):
        raise SystemExit(f"uncommitted changes; commit first (or LANDMARK_ALLOW_DIRTY=1):\n{out}")


def require_ac_power() -> None:
    """Refuse to benchmark on battery: macOS throttles, so timings would mislead."""
    out = subprocess.run(["pmset", "-g", "batt"], capture_output=True, text=True).stdout
    if "Battery Power" in out and not os.environ.get("LANDMARK_ALLOW_BATTERY"):
        raise SystemExit(
            "on battery power: plug in, or set LANDMARK_ALLOW_BATTERY=1 for a labeled dry run"
        )


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--manifest", default=str(ROOT / "eval/manifests/splits_small_seed0.json"))
    ap.add_argument("--tracks", type=int, default=600)
    ap.add_argument("--workers", type=int, nargs="+", default=[1, 2, 4, 8, 12])
    ap.add_argument("--out", required=True)
    args = ap.parse_args()
    require_ac_power()
    require_clean_tree()

    man = json.loads(Path(args.manifest).read_text())
    sub = {"meta": man["meta"], "indexed": sorted(man["indexed"])[: args.tracks], "clusters": {}}
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    rows = []
    with tempfile.TemporaryDirectory() as tmp:
        mpath = Path(tmp) / "manifest.json"
        mpath.write_text(json.dumps(sub))
        for w in args.workers:
            idx = Path(tmp) / f"w{w}"
            subprocess.run(
                [str(ROOT / "bin/ingest"), "-manifest", str(mpath), "-out", str(idx),
                 "-partitions", "1", "-workers", str(w)],
                check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            )  # fmt: skip
            stats = json.loads((idx / "ingest_stats.json").read_text())
            (out / f"workers_{w}.json").write_text(json.dumps(stats, indent=1) + "\n")
            fp = stats["fingerprint_wall_seconds"]
            row = {
                "workers": w,
                "tracks": stats["tracks"],
                "fingerprint_wall_s": round(fp, 2),
                "write_wall_s": round(stats["write_wall_seconds"], 2),
                "tracks_per_min": round(60 * stats["tracks"] / fp, 1),
                "audio_x_realtime": round(stats["audio_seconds"] / fp, 1),
            }
            rows.append(row)
            print(row, flush=True)
    base = rows[0]["tracks_per_min"]
    for r in rows:
        r["speedup_vs_first"] = round(r["tracks_per_min"] / base, 2)
    (out / "summary.json").write_text(json.dumps(rows, indent=1) + "\n")


if __name__ == "__main__":
    main()
