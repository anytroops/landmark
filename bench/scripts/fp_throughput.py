"""Python fingerprinting throughput, same method as bench/fpbench (Go).

Tracks are decoded up front (in each worker, before its timer starts), so
only fingerprinting is timed. All-cores: equal static chunks, one process
each, wall time = slowest worker. Output: bench/results/fp_throughput_python.json
"""

import argparse
import json
import os
import time
from multiprocessing import Pool
from pathlib import Path

from prototype import fingerprint as fp
from prototype.audio import SAMPLE_RATE, DecodeError, decode
from prototype.build_index import machine_spec
from prototype.fma import audio_path

ROOT = Path(__file__).resolve().parents[2]


def _chunk(ids: list[int]) -> tuple[float, float, int]:
    """Decode a chunk, then time fingerprinting it. Returns (seconds, audio_s, landmarks)."""
    pcms = []
    for fid in ids:
        try:
            pcms.append(decode(audio_path(fid)))
        except DecodeError:
            continue
    start = time.perf_counter()
    n_lm = sum(len(fp.fingerprint(x)[0]) for x in pcms)
    return time.perf_counter() - start, sum(len(x) for x in pcms) / SAMPLE_RATE, n_lm


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--n", type=int, default=200)
    ap.add_argument("--reps", type=int, default=3)
    args = ap.parse_args()

    manifest = json.loads((ROOT / "eval/manifests/splits_small_seed0.json").read_text())
    ids = manifest["indexed"][: args.n]
    cores = os.cpu_count() or 1
    runs, audio_s, landmarks = [], 0.0, 0
    for workers in (1, cores):
        chunks = [ids[w::workers] for w in range(workers)]
        for rep in range(args.reps):
            with Pool(workers) as pool:
                out = pool.map(_chunk, chunks)
            wall = max(o[0] for o in out)
            audio_s, landmarks = sum(o[1] for o in out), sum(o[2] for o in out)
            runs.append(
                {
                    "workers": workers,
                    "rep": rep,
                    "wall_seconds": wall,
                    "audio_seconds_per_wall_second": audio_s / wall,
                    "ms_per_30s_clip_per_worker": 1000 * wall * workers / (audio_s / 30),
                }
            )
            print(f"workers={workers} rep={rep} wall={wall:.2f}s {audio_s / wall:.0f}x realtime")
    res = {
        "lang": "python",
        "tracks": len(ids),
        "audio_seconds": audio_s,
        "landmarks": landmarks,
        "runs": runs,
        "machine": machine_spec(),
        "query_shifts": 1,
        "manifest_first_n": args.n,
    }
    out_path = ROOT / "bench/results/fp_throughput_python.json"
    out_path.write_text(json.dumps(res, indent=2) + "\n")


if __name__ == "__main__":
    main()
