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
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from collections.abc import Iterator
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
# LANDMARK_BIN runs other binaries under this harness, e.g. ones built from an
# older commit in a separate worktree for a before/after comparison; each
# run's harness.json records which commit that directory's repo is at.
BIN = Path(os.environ.get("LANDMARK_BIN", ROOT / "bin")).resolve()


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
    index: Path,
    rf: int,
    deadline_ms: int,
    workers: int,
    base_port: int = 7100,
    shard_procs: int = 0,
    stop_fraction: float = 0.0,
) -> Iterator[tuple[str, list[subprocess.Popen]]]:
    """Start rf replicas of every partition in index/ plus a coordinator.

    stop_fraction > 0 turns on the coordinator's stop list (D30).

    shard_procs > 0 sets GOMAXPROCS for each shard (default: all cores each,
    which oversubscribes the host when many shards share it).
    """
    env = dict(os.environ)
    if shard_procs > 0:
        env["GOMAXPROCS"] = str(shard_procs)
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
                        stdout=log.open("w"), stderr=subprocess.STDOUT, env=env,
                    )
                )  # fmt: skip
                wait_for(log, "serving")
                replicas.append(addr)
                port += 1
            partitions.append({"id": seg["partition"], "replicas": replicas})
        cfg = {"index_dir": str(index.resolve()), "deadline_ms": deadline_ms, "t_abs": 30,
               "t_ratio": 1.0, "stop_fraction": stop_fraction,
               "partitions": partitions}  # fmt: skip
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


CHARGING = ("charging", "finishing charge")


def battery() -> tuple[str, int, str]:
    """Power source, charge percent, and charge state, e.g. ("ac", 100, "charged")."""
    out = subprocess.run(["pmset", "-g", "batt"], capture_output=True, text=True).stdout
    return parse_battery(out)


def parse_battery(out: str) -> tuple[str, int, str]:
    """Parse `pmset -g batt` output; (source, -1, "none") without a battery."""
    source = "ac" if "AC Power" in out else "battery" if "Battery Power" in out else "unknown"
    m = re.search(r"(\d+)%; ([^;]+);", out)
    return (source, int(m.group(1)), m.group(2).strip()) if m else (source, -1, "none")


def wait_until_not_charging(poll_s: float = 30) -> tuple[str, int, str]:
    """Block while the battery charges, then return the battery state.

    Charging draws from the same adapter as the CPU. On this laptop's 65 W
    adapter, P=1 at 550 qps failed all three B1 runs while the battery charged
    from 82%, after passing B2 at full charge.
    """
    announced = False
    while True:
        b = battery()
        if os.environ.get("LANDMARK_ALLOW_BATTERY"):
            return b
        if b[0] != "ac":
            raise SystemExit("on battery power: plug in, or set LANDMARK_ALLOW_BATTERY=1")
        if b[2] not in CHARGING:
            return b
        if not announced:
            print(f"battery {b[1]}% {b[2]}: waiting until it stops charging", flush=True)
            announced = True
        time.sleep(poll_s)


def run_loadgen(target: str, queries: Path, rate: float, warmup: int, duration: int,
                out: Path, label: str, extra: list[str]) -> dict:  # fmt: skip
    """Run one load test, after waiting for the battery to stop charging.

    The run is invalid (its directory renamed to *.invalid, and the harness
    stops) if the machine slept, left AC power, or the battery drained or
    began charging during it. macOS's monotonic clock stops while the machine
    sleeps and the wall clock does not, so a gap between them means processes
    were frozen mid-run and every latency in it is meaningless. A battery that
    drains on AC means the adapter could not supply the load. Battery state
    before and after goes to harness.json next to loadgen's summary.json.
    """
    cmd = [str(BIN / "loadgen"), "run", "-target", target, "-queries", str(queries),
           "-rate", str(rate), "-warmup", f"{warmup}s", "-duration", f"{duration}s",
           "-label", label, "-out", str(out), *extra]  # fmt: skip
    before = wait_until_not_charging()
    wall0, mono0 = time.time(), time.monotonic()
    subprocess.run(cmd, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    slept = (time.time() - wall0) - (time.monotonic() - mono0)
    after = battery()
    summary = json.loads((out / "summary.json").read_text())

    problems = []
    if slept > 2:
        problems.append(f"machine asleep ~{slept:.0f} s")
    if not os.environ.get("LANDMARK_ALLOW_BATTERY"):
        if summary["machine"]["power"] != "ac" or after[0] != "ac":
            problems.append("left AC power")
        if after[1] < before[1]:
            problems.append(f"battery fell {before[1]}% to {after[1]}% on AC")
        if after[2] in CHARGING:
            problems.append(f"battery began charging ({after[1]}%)")
    bin_commit = subprocess.run(["git", "-C", str(BIN), "rev-parse", "--short", "HEAD"],
                                capture_output=True, text=True).stdout.strip()  # fmt: skip
    harness = {"bin": str(BIN), "bin_repo_commit": bin_commit or "unknown",
               "battery_before": f"{before[1]}%; {before[2]}",
               "battery_after": f"{after[1]}%; {after[2]}",
               "slept_s": round(slept, 1), "valid": not problems, "problems": problems}  # fmt: skip
    (out / "harness.json").write_text(json.dumps(harness, indent=1) + "\n")
    if problems:
        bad = out.with_name(out.name + ".invalid")
        shutil.rmtree(bad, ignore_errors=True)
        out.rename(bad)
        raise SystemExit(f"run {out} is invalid ({'; '.join(problems)}); moved to {bad.name}. "
                         "Plug in, keep the lid open, rerun.")  # fmt: skip
    return summary


def sweep(args: argparse.Namespace) -> None:
    index = Path(args.index)
    n_parts = json.loads((index / "index.json").read_text())["n_partitions"]
    with cluster(index, args.rf, args.deadline_ms, args.workers, shard_procs=args.shard_procs,
                 stop_fraction=args.stop_fraction) as (target, _):  # fmt: skip
        for rate in args.rates:
            for rep in range(args.reps):
                out = Path(args.out) / f"P{n_parts}_rf{args.rf}_r{rate:g}_rep{rep}"
                label = (f"single-host bare metal, P={n_parts} RF={args.rf}, workers/shard="
                         f"{args.workers}, GOMAXPROCS/shard={args.shard_procs or 'all'}, "
                         f"deadline {args.deadline_ms} ms, "
                         f"stop_fraction {args.stop_fraction:g}")  # fmt: skip
                s = run_loadgen(target, Path(args.queries), rate, args.warmup, args.duration,
                                out, label, args.loadgen_args)  # fmt: skip
                m = s["measured"]
                print(f"P={n_parts} rate={rate:g} rep={rep}: success={m['success_rate']:.4f} "
                      f"p50={m['p50_ms']} p99={m['p99_ms']} p999={m['p999_ms']} "
                      f"codes={m['codes']}", flush=True)  # fmt: skip


def profile(args: argparse.Namespace) -> None:
    """Run load, and mid-run capture CPU profiles plus per-process CPU shares."""
    import urllib.request

    index = Path(args.index)
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    base = 7100
    n_parts = json.loads((index / "index.json").read_text())["n_partitions"]
    with cluster(index, args.rf, args.deadline_ms, args.workers, base, args.shard_procs,
                 stop_fraction=args.stop_fraction) as (target, procs):  # fmt: skip
        lg = subprocess.Popen(
            [str(BIN / "loadgen"), "run", "-target", target, "-queries", args.queries,
             "-rate", str(args.rate), "-warmup", "10s", "-duration", f"{args.seconds + 10}s",
             "-label", f"profile run, P={n_parts}",
             "-out", str(out / "loadgen")],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )  # fmt: skip
        time.sleep(10)
        names = {p.pid: Path(p.args[0]).name for p in procs} | {lg.pid: "loadgen"}
        ps = subprocess.run(["ps", "-o", "pid=,%cpu=", "-p", ",".join(map(str, names))],
                            capture_output=True, text=True, check=True).stdout  # fmt: skip

        def grab(port: int, name: str) -> None:
            url = f"http://127.0.0.1:{port}/debug/pprof/profile?seconds={args.seconds}"
            with urllib.request.urlopen(url, timeout=args.seconds + 30) as r:
                (out / f"{name}.pprof").write_bytes(r.read())

        import threading

        threads = [threading.Thread(target=grab, args=(base + 501, "coordinator")),
                   threading.Thread(target=grab, args=(base + 1000, "shard0"))]  # fmt: skip
        for t in threads:
            t.start()
        samples = []
        for _ in range(args.seconds // 2):
            time.sleep(2)
            samples.append(subprocess.run(
                ["ps", "-o", "pid=,%cpu=", "-p", ",".join(map(str, names))],
                capture_output=True, text=True, check=True).stdout)  # fmt: skip
        for t in threads:
            t.join()
        lg.wait()
    shares: dict[str, list[float]] = {}
    for snap in [ps, *samples]:
        per: dict[str, float] = {}
        for line in snap.split("\n"):
            if line.strip():
                pid, cpu = line.split()
                per[names[int(pid)]] = per.get(names[int(pid)], 0.0) + float(cpu)
        for k, v in per.items():
            shares.setdefault(k, []).append(v)
    avg = {k: round(sum(v) / len(v), 1) for k, v in shares.items()}
    (out / "process_cpu.json").write_text(json.dumps({"percent_of_one_core": avg}, indent=1))
    print("average CPU (% of one core):", avg)


def equiv(args: argparse.Namespace) -> None:
    """Run two clusters side by side and check they answer identically."""
    a, b = Path(args.index_a), Path(args.index_b)
    with (
        cluster(a, 1, args.deadline_ms, args.workers, base_port=7100,
                stop_fraction=args.stop_fraction) as (addr_a, _),
        cluster(b, 1, args.deadline_ms, args.workers, base_port=7300,
                stop_fraction=args.stop_fraction) as (addr_b, _),
    ):  # fmt: skip
        label = f"{a.name} vs {b.name}, stop_fraction {args.stop_fraction:g}"
        subprocess.run([str(BIN / "equiv"), "-a", addr_a, "-b", addr_b, "-queries", args.queries,
                        "-label", label, "-out", args.out], check=True)  # fmt: skip


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
    sw.add_argument("--shard-procs", type=int, default=0, help="GOMAXPROCS per shard (0 = all)")
    sw.add_argument("--stop-fraction", type=float, default=0.0, help="coordinator stop list (D30)")
    sw.add_argument("loadgen_args", nargs="*", help="extra flags passed to loadgen run after --")
    pr = sub.add_parser("profile")
    pr.add_argument("--index", required=True)
    pr.add_argument("--rf", type=int, default=1)
    pr.add_argument("--rate", type=float, required=True)
    pr.add_argument("--seconds", type=int, default=20)
    pr.add_argument("--deadline-ms", type=int, default=200)
    pr.add_argument("--workers", type=int, default=os.cpu_count() or 1)
    pr.add_argument("--queries", default=str(ROOT / "data/queries/test_10s_seed0.gob"))
    pr.add_argument("--out", required=True)
    pr.add_argument("--shard-procs", type=int, default=0, help="GOMAXPROCS per shard (0 = all)")
    pr.add_argument("--stop-fraction", type=float, default=0.0, help="coordinator stop list (D30)")
    eq = sub.add_parser("equiv", help="check two indexes answer identically (e.g. P=1 vs P=8)")
    eq.add_argument("--index-a", required=True)
    eq.add_argument("--index-b", required=True)
    eq.add_argument("--stop-fraction", type=float, default=0.0)
    eq.add_argument("--deadline-ms", type=int, default=5000, help="generous: no load, no partials")
    eq.add_argument("--workers", type=int, default=os.cpu_count() or 1)
    eq.add_argument("--queries", default=str(ROOT / "data/queries/medium_test_10s_seed0.gob"))
    eq.add_argument("--out", required=True)
    args = ap.parse_args()
    require_ac_power()
    require_clean_tree()
    if args.cmd == "sweep":
        sweep(args)
    elif args.cmd == "profile":
        profile(args)
    elif args.cmd == "equiv":
        equiv(args)
    else:
        sys.exit(2)


if __name__ == "__main__":
    main()
