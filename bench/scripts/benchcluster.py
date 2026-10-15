"""Run a bare-metal cluster (shards + coordinator as local processes) and
drive it with bin/loadgen.

Docker on this laptop runs in a 4-vCPU VM, which would cap throughput
benchmarks; these run on the host's cores directly. Everything shares one
machine (shards, coordinator, load generator), so partition scaling here
measures parallelism on one host, not distribution. Results say so.

  # one cluster, sweep offered load:
  python -m bench.scripts.benchcluster sweep --index data/segments/p2_varint \
      --rates 100 200 400 --warmup 10 --duration 30 --reps 1 --out bench/results/b1_quick

Writes one loadgen output directory per (rate, rep) under --out.
"""

import argparse
import contextlib
import json
import os
import signal
import subprocess
import sys
import tempfile
import time
from collections.abc import Iterator
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BIN = ROOT / "bin"


def wait_for(log: Path, needle: str, timeout_s: float = 30.0) -> None:
    deadline = time.monotonic() + timeout_s
    while time.monotonic() < deadline:
        if log.exists() and needle in log.read_text(errors="replace"):
            return
        time.sleep(0.05)
    raise TimeoutError(
        f"{needle!r} not seen in {log} after {timeout_s}s:\n{log.read_text()[-2000:]}"
    )


@contextlib.contextmanager
def cluster(
    index: Path, rf: int, deadline_ms: int, workers: int, base_port: int = 7100
) -> Iterator[tuple[str, list[subprocess.Popen]]]:
    """Start rf replicas of every partition in index/ plus a coordinator."""
    man = json.loads((index / "index.json").read_text())
    tmp = Path(tempfile.mkdtemp(prefix="benchcluster-"))
    procs: list[subprocess.Popen] = []
    partitions = []
    port = base_port
    try:
        for seg in sorted(man["segments"], key=lambda s: s["partition"]):
            replicas = []
            for r in range(rf):
                addr = f"127.0.0.1:{port}"
                log = tmp / f"shard-{seg['partition']}-{r}.log"
                procs.append(
                    subprocess.Popen(
                        [str(BIN / "shard"), "-segment", str(index / seg["file"]), "-listen", addr,
                         "-admin", f"127.0.0.1:{port + 1000}", "-workers", str(workers)],
                        stdout=log.open("w"), stderr=subprocess.STDOUT,
                    )
                )  # fmt: skip
                wait_for(log, "serving")
                replicas.append(addr)
                port += 1
            partitions.append({"id": seg["partition"], "replicas": replicas})
        cfg = {"index_dir": str(index.resolve()), "deadline_ms": deadline_ms, "t_abs": 30,
               "t_ratio": 1.0, "partitions": partitions}  # fmt: skip
        (tmp / "cluster.json").write_text(json.dumps(cfg, indent=1))
        coord_addr = f"127.0.0.1:{base_port + 500}"
        clog = tmp / "coordinator.log"
        procs.append(
            subprocess.Popen(
                [str(BIN / "coordinator"), "-config", str(tmp / "cluster.json"),
                 "-listen", coord_addr, "-admin", f"127.0.0.1:{base_port + 501}"],
                stdout=clog.open("w"), stderr=subprocess.STDOUT,
            )
        )  # fmt: skip
        wait_for(clog, "serving")
        yield coord_addr, procs
    finally:
        for p in procs:
            if p.poll() is None:
                p.send_signal(signal.SIGTERM)
        for p in procs:
            try:
                p.wait(timeout=15)
            except subprocess.TimeoutExpired:
                p.kill()


def run_loadgen(target: str, queries: Path, rate: float, warmup: int, duration: int,
                out: Path, label: str, extra: list[str]) -> dict:  # fmt: skip
    cmd = [str(BIN / "loadgen"), "run", "-target", target, "-queries", str(queries),
           "-rate", str(rate), "-warmup", f"{warmup}s", "-duration", f"{duration}s",
           "-label", label, "-out", str(out), *extra]  # fmt: skip
    subprocess.run(cmd, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    return json.loads((out / "summary.json").read_text())


def sweep(args: argparse.Namespace) -> None:
    index = Path(args.index)
    n_parts = json.loads((index / "index.json").read_text())["n_partitions"]
    with cluster(index, args.rf, args.deadline_ms, args.workers) as (target, _):
        for rate in args.rates:
            for rep in range(args.reps):
                out = Path(args.out) / f"P{n_parts}_rf{args.rf}_r{rate:g}_rep{rep}"
                label = (f"single-host bare metal, P={n_parts} RF={args.rf}, workers/shard="
                         f"{args.workers}, deadline {args.deadline_ms} ms")  # fmt: skip
                s = run_loadgen(target, Path(args.queries), rate, args.warmup, args.duration,
                                out, label, args.loadgen_args)  # fmt: skip
                m = s["measured"]
                print(f"P={n_parts} rate={rate:g} rep={rep}: success={m['success_rate']:.4f} "
                      f"p50={m['p50_ms']} p99={m['p99_ms']} p999={m['p999_ms']} "
                      f"codes={m['codes']}", flush=True)  # fmt: skip


def main() -> None:
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    sw = sub.add_parser("sweep")
    sw.add_argument("--index", required=True)
    sw.add_argument("--rf", type=int, default=1)
    sw.add_argument("--rates", type=float, nargs="+", required=True)
    sw.add_argument("--warmup", type=int, default=30)
    sw.add_argument("--duration", type=int, default=120)
    sw.add_argument("--reps", type=int, default=3)
    sw.add_argument("--deadline-ms", type=int, default=200)
    sw.add_argument("--workers", type=int, default=os.cpu_count() or 1)
    sw.add_argument("--queries", default=str(ROOT / "data/queries/test_10s_seed0.gob"))
    sw.add_argument("--out", required=True)
    sw.add_argument("loadgen_args", nargs="*", help="extra flags passed to loadgen run after --")
    args = ap.parse_args()
    if args.cmd == "sweep":
        sweep(args)
    else:
        sys.exit(2)


if __name__ == "__main__":
    main()
