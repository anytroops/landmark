"""What delta segments cost a search (D42; docs/design/throughput.md T2).

  python -m bench.scripts.delta_cost --songs 1000 --deltas 0 1 10 40 --rate 400 \\
      --out bench/results/b15_delta_cost

The same never-indexed fma_medium tracks (val and test negatives, as
bench.scripts.live_writes adds) are added to APFS clones of the fma_medium
pair index (P=1), in 1, 10, or 40 batches of equal size: one delta segment
each. Layout 0 adds them in one batch and merges it into the base, so all
layouts hold the same postings. Each layout is served by one shard
following its manifests (shard -index) behind a coordinator with the 4%
stop list, all at once; bin/loadgen then offers each in turn a fixed rate
of the 10 s test queries, the layouts taking turns within each rep so drift
hits all alike. Per layout and rep: loadgen's latency percentiles, the
shard's CPU time per search (from ps, before and after), and its mean
postings scanned per search. Needs AC power with the battery not charging.
"""

import argparse
import json
import shutil
import subprocess
import tempfile
import time
from pathlib import Path

from bench.scripts.benchcluster import (
    BIN,
    ROOT,
    require_ac_power,
    require_clean_tree,
    run_loadgen,
    wait_for,
    wait_until_not_charging,
)
from bench.scripts.live_writes import INDEX, MANIFEST, QUERIES, audio, scrape

WORK = ROOT / "data/deltacost"


def cpu_seconds(pid: int) -> float:
    """A process's user plus system CPU time so far (ps, centiseconds)."""
    out = subprocess.run(["ps", "-o", "time=", "-p", str(pid)], capture_output=True, text=True)
    t = out.stdout.strip()  # [[dd-]hh:]mm:ss.cc
    days, _, rest = t.rpartition("-")
    parts = [float(x) for x in rest.split(":")]
    secs = sum(v * 60**i for i, v in enumerate(reversed(parts)))
    return secs + (float(days) * 86400 if days else 0)


def segments(d: Path) -> int:
    """Segments in the newest manifest's partition 0."""
    newest = max(d.glob("manifest-*.json"))
    return len(json.loads(newest.read_text())["partitions"][0])


def build(layout: int, songs: list[int], workers: int) -> Path:
    """A clone of the pair index with songs added as layout delta segments
    (0: added as one and merged into the base)."""
    d = WORK / f"deltas{layout:02d}"
    subprocess.run(["cp", "-cR", str(INDEX), str(d)], check=True)  # clone: shares blocks
    for f in d.glob("manifest-*.json"):
        f.unlink()
    n = max(layout, 1)
    size = -(-len(songs) // n)
    for k in range(n):
        batch = songs[k * size : (k + 1) * size]
        tsv = d.parent / f"deltas{layout:02d}-add{k:02d}.tsv"
        tsv.write_text("".join(f"{f}\t{audio(f)}\n" for f in batch))
        subprocess.run([str(BIN / "ingest"), "-add", str(tsv), "-out", str(d), "-workers",
                        str(workers)], check=True, stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL)  # fmt: skip
    if layout == 0:
        subprocess.run([str(BIN / "ingest"), "-merge", "-out", str(d)], check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)  # fmt: skip
    return d


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--songs", type=int, default=1000)
    ap.add_argument("--deltas", type=int, nargs="+", default=[0, 1, 10, 40])
    ap.add_argument("--rate", type=float, default=400, help="offered queries per second")
    ap.add_argument("--warmup", type=int, default=10)
    ap.add_argument("--duration", type=int, default=60)
    ap.add_argument("--reps", type=int, default=3)
    ap.add_argument("--shard-workers", type=int, default=8)
    ap.add_argument("--ingest-workers", type=int, default=8)
    ap.add_argument("--out", type=Path, required=True)
    args = ap.parse_args()
    require_clean_tree()
    require_ac_power()
    wait_until_not_charging()
    m = json.loads(MANIFEST.read_text())
    songs = sorted(int(f) for f in m["val_neg"] + m["test_neg"])[: args.songs]
    if WORK.exists():
        shutil.rmtree(WORK)
    WORK.mkdir(parents=True)
    dirs = {n: build(n, songs, args.ingest_workers) for n in args.deltas}

    tmp = Path(tempfile.mkdtemp(prefix="deltacost-"))
    procs: list[subprocess.Popen] = []
    serving = {}
    try:
        for j, (n, d) in enumerate(dirs.items()):
            port, admin = 8600 + 10 * j, 9600 + 10 * j
            log = tmp / f"shard{n}.log"
            shard = subprocess.Popen(
                [str(BIN / "shard"), "-index", str(d), "-partition", "0", "-listen",
                 f"127.0.0.1:{port}", "-admin", f"127.0.0.1:{admin}",
                 "-workers", str(args.shard_workers)],
                stdout=log.open("w"), stderr=subprocess.STDOUT)  # fmt: skip
            procs.append(shard)
            wait_for(log, "serving", 120)
            cfg = {"index_dir": str(d), "deadline_ms": 1000, "t_abs": 30, "t_ratio": 1.0,
                   "stop_fraction": 0.04,
                   "partitions": [{"id": 0, "replicas": [f"127.0.0.1:{port}"]}]}  # fmt: skip
            (tmp / f"cluster{n}.json").write_text(json.dumps(cfg))
            coord = f"127.0.0.1:{port + 5}"
            clog = tmp / f"coordinator{n}.log"
            procs.append(subprocess.Popen(
                [str(BIN / "coordinator"), "-config", str(tmp / f"cluster{n}.json"),
                 "-listen", coord, "-admin", f"127.0.0.1:{admin + 5}"],
                stdout=clog.open("w"), stderr=subprocess.STDOUT))  # fmt: skip
            wait_for(clog, "serving")
            serving[n] = {"coord": coord, "admin": admin, "pid": shard.pid,
                          "segments": segments(d)}  # fmt: skip
        runs = []
        for rep in range(args.reps):
            for n, s in serving.items():
                out = args.out / f"deltas{n:02d}_rep{rep}"
                searches0 = scrape(s["admin"], "landmark_shard_postings_scanned_count")
                postings0 = scrape(s["admin"], "landmark_shard_postings_scanned_sum")
                cpu0 = cpu_seconds(s["pid"])
                summary = run_loadgen(s["coord"], QUERIES, args.rate, args.warmup,
                                      args.duration, out, f"{n} deltas", [])  # fmt: skip
                cpu1 = cpu_seconds(s["pid"])
                searches = scrape(s["admin"], "landmark_shard_postings_scanned_count") - searches0
                postings = scrape(s["admin"], "landmark_shard_postings_scanned_sum") - postings0
                got = summary["measured"]
                row = {"deltas": n, "rep": rep, "p50_ms": got["p50_ms"], "p90_ms": got["p90_ms"],
                       "p99_ms": got["p99_ms"], "count": got["count"],
                       "success_rate": got["success_rate"],
                       "shard_cpu_ms_per_search": 1000 * (cpu1 - cpu0) / max(searches, 1),
                       "postings_per_search": postings / max(searches, 1),
                       "searches": searches}  # fmt: skip
                runs.append(row)
                print(json.dumps(row), flush=True)
                time.sleep(2)
    finally:
        for p in procs:
            if p.poll() is None:
                p.terminate()
        for p in procs:
            p.wait(timeout=30)
    result = {"songs": len(songs), "rate": args.rate, "duration_s": args.duration,
              "reps": args.reps, "layouts": {str(n): {"segments": s["segments"]}
                                             for n, s in serving.items()},
              "runs": runs}  # fmt: skip
    (args.out / "result.json").write_text(json.dumps(result, indent=1) + "\n")
    shutil.rmtree(WORK)


if __name__ == "__main__":
    main()
