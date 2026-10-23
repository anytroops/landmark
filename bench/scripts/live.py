"""Run live monitoring end to end on local processes (D38-D40).

  # Live output equals offline output, mix for mix:
  python -m bench.scripts.live equiv --sets set044 --out eval/results/live/equiv_set044

  # Real-time stations (test mixes back to back), two workers, one killed:
  python -m bench.scripts.live realtime --stations 16 --per-station 3 --workers 2 \
      --capacity 16 --kill-at 120 --out bench/results/live/rt16_kill

equiv starts the triplet and pair clusters (benchcluster), runs
bin/tracklist -json on every mix of the sets (offline), then runs the
registry and one bin/monitor worker with every mix as a file stream read as
fast as possible, and compares each stream's logged entries with the
offline ones. Writes offline_runs.jsonl and live_runs.jsonl (eval.score_cli
format), the registry's event logs, and result.json.
"""

import argparse
import concurrent.futures
import itertools
import json
import os
import random
import shutil
import signal
import subprocess
import tempfile
import time
import urllib.request
from pathlib import Path

from bench.scripts.benchcluster import (
    BIN,
    CHARGING,
    ROOT,
    battery,
    cluster,
    require_ac_power,
    require_clean_tree,
    wait_for,
    wait_until_not_charging,
)
from eval import live_score

B2_INDEX = ROOT / "data/segments/medium_unmixdb_b2"
PAIR_INDEX = ROOT / "data/segments/medium_unmixdb"


def run_out(*cmd: str) -> str:
    return subprocess.run(cmd, capture_output=True, text=True).stdout.strip()


def git_head() -> dict:
    status = run_out("git", "-C", str(ROOT), "status", "--porcelain", "--untracked-files=no")
    return {"commit": run_out("git", "-C", str(ROOT), "rev-parse", "HEAD"), "dirty": bool(status)}


def mixes(name: str, sets: list[str]) -> list[str]:
    gt = json.loads((ROOT / "eval" / "mixes" / f"{name}.json").read_text())
    return [m["file"] for m in gt["mixes"] if m["set"] in sets]


def offline(files: list[str], b2: str, pairs: str, parallel: int, timeout: str,
            hop: float) -> dict[str, list]:  # fmt: skip
    def one(f: str) -> tuple[str, list]:
        out = subprocess.run(
            [str(BIN / "tracklist"), "-coordinator", b2, "-index", str(B2_INDEX),
             "-pairs-coordinator", pairs, "-pairs-index", str(PAIR_INDEX), "-timeout", timeout,
             "-hop", repr(hop), "-json", str(ROOT / f)],
            capture_output=True, text=True, check=True,
        ).stdout  # fmt: skip
        return f, json.loads(out)

    with concurrent.futures.ThreadPoolExecutor(parallel) as ex:
        return dict(ex.map(one, files))


def live(files: list[str], b2: str, pairs: str, out: Path, capacity: int, timeout: str,
         timeout_s: float, hop: float) -> dict[str, list]:  # fmt: skip
    tmp = Path(tempfile.mkdtemp(prefix="live-"))
    streams = [{"id": Path(f).stem, "url": str(ROOT / f)} for f in files]
    (tmp / "streams.json").write_text(json.dumps(streams))
    reg_dir = tmp / "registry"
    procs = []
    try:
        rlog = tmp / "registry.log"
        procs.append(subprocess.Popen(
            [str(BIN / "registry"), "-dir", str(reg_dir), "-streams", str(tmp / "streams.json"),
             "-listen", "127.0.0.1:7900", "-admin", "127.0.0.1:7901"],
            stdout=rlog.open("w"), stderr=subprocess.STDOUT))  # fmt: skip
        wait_for(rlog, "serving")
        mlog = tmp / "monitor.log"
        procs.append(subprocess.Popen(
            [str(BIN / "monitor"), "-registry", "127.0.0.1:7900", "-id", "w1",
             "-capacity", str(capacity), "-read-rate", "0", "-timeout", timeout, "-hop", repr(hop),
             "-coordinator", b2, "-index", str(B2_INDEX),
             "-pairs-coordinator", pairs, "-pairs-index", str(PAIR_INDEX),
             "-window-log", str(out / "windows.jsonl"), "-admin", "127.0.0.1:7902"],
            stdout=mlog.open("w"), stderr=subprocess.STDOUT))  # fmt: skip
        deadline = time.monotonic() + timeout_s
        while True:
            ended = mlog.read_text(errors="replace").count("stream source ended")
            if ended >= len(files):
                break
            if time.monotonic() > deadline or any(p.poll() is not None for p in procs):
                raise SystemExit(f"live run stopped: {ended}/{len(files)} streams ended; see {tmp}")
            time.sleep(2)
    finally:
        for p in procs:
            if p.poll() is None:
                p.send_signal(signal.SIGTERM)
        for p in procs:
            p.wait(timeout=30)
    shutil.copytree(reg_dir / "events", out / "events", dirs_exist_ok=True)
    shutil.copy(mlog, out / "monitor.log")
    entries: dict[str, list] = {}
    by_id = {Path(f).stem: f for f in files}
    for log in sorted((out / "events").glob("*.jsonl")):
        f = by_id[log.stem]
        entries[f] = []
        for line in log.read_text().splitlines():
            e = json.loads(line)
            if e["kind"] != "EVENT_KIND_END":
                continue
            base = e["sourceStartUnix"]
            entries[f].append({"track": e.get("trackId", 0), "name": e.get("name", ""),
                               "start_s": e["startUnix"] - base, "end_s": e["endUnix"] - base,
                               "tempo_pct": 100 * (e["tempo"] - 1),
                               "evidence": e.get("evidence", 0.0)})  # fmt: skip
    return entries


def same(a: dict, b: dict) -> bool:
    return (a["track"] == b["track"] and abs(a["start_s"] - b["start_s"]) < 1e-3
            and abs(a["end_s"] - b["end_s"]) < 1e-3 and abs(a["tempo_pct"] - b["tempo_pct"]) < 1e-6
            and a["evidence"] == b["evidence"])  # fmt: skip


def equiv(args: argparse.Namespace) -> None:
    out = args.out
    out.mkdir(parents=True, exist_ok=True)
    files = mixes(args.mixes, args.sets)[: args.limit or None]
    head = git_head()
    with (
        cluster(
            B2_INDEX,
            1,
            args.deadline_ms,
            args.workers,
            base_port=7100,
            stop_fraction=args.stop_fraction,
        ) as (b2, _),
        cluster(
            PAIR_INDEX,
            1,
            args.deadline_ms,
            args.workers,
            base_port=7200,
            stop_fraction=args.stop_fraction,
        ) as (pairs, _),
    ):
        t0 = time.monotonic()
        off = offline(files, b2, pairs, args.parallel, args.timeout, args.hop)
        t_off = time.monotonic() - t0
        t0 = time.monotonic()
        on = live(files, b2, pairs, out, args.capacity, args.timeout, args.live_timeout_s, args.hop)
        t_on = time.monotonic() - t0
    for name, runs in (("offline_runs.jsonl", off), ("live_runs.jsonl", on)):
        with (out / name).open("w") as fh:
            for f in files:
                fh.write(json.dumps({"file": f, "entries": runs.get(f, [])}) + "\n")
    differ = [f for f in files if len(off[f]) != len(on.get(f, []))
              or not all(same(a, b) for a, b in zip(off[f], on[f], strict=True))]  # fmt: skip
    windows = [json.loads(x) for x in (out / "windows.jsonl").read_text().splitlines()]
    result = {
        "mixes": len(files), "sets": args.sets, "mixes_differing": len(differ), "differing": differ,
        "entries_offline": sum(len(v) for v in off.values()),
        "entries_live": sum(len(v) for v in on.values()),
        "windows_live": len(windows), "windows_unanswered": sum(w["failed"] for w in windows),
        "offline_wall_s": round(t_off, 1), "live_wall_s": round(t_on, 1),
        "capacity": args.capacity, "parallel_offline": args.parallel, "hop_s": args.hop,
        "stop_fraction": args.stop_fraction, **head,
        "power": run_out("pmset", "-g", "batt").splitlines()[:1],
    }  # fmt: skip
    (out / "result.json").write_text(json.dumps(result, indent=1) + "\n")
    print(json.dumps({k: v for k, v in result.items() if k != "differing"}, indent=1))


TEST_SETS = ["set044", "set123", "set230", "set281"]


def lag_stats(paths: list[Path], hop: float) -> dict:
    """Window lag (last sample in to searches done) and the longest gap
    between consecutive windows of one stream arriving; a gap well over the
    hop means audio delivery stalled (a sleeping machine freezes every
    process, then the stream server sends the backlog at once), which makes
    the run's timings invalid."""
    w = [{**json.loads(x), "holder": i} for i, p in enumerate(paths) if p.exists()
         for x in p.read_text().splitlines()]  # fmt: skip
    lag = sorted(x["done_unix"] - x["ready_unix"] for x in w)
    if not lag:
        return {"windows": 0}

    def q(p: float) -> float:
        return round(lag[min(len(lag) - 1, int(p * len(lag)))], 3)

    ready: dict[tuple, list[float]] = {}  # per holder: a failover gap is not a stall
    for x in w:
        ready.setdefault((x["holder"], x["stream"]), []).append(x["ready_unix"])
    gap = max((b - a for r in ready.values() for a, b in itertools.pairwise(sorted(r))), default=0)
    return {"windows": len(w), "unanswered": sum(x["failed"] for x in w),
            "lag_s": {"p50": q(0.5), "p90": q(0.9), "p99": q(0.99), "max": round(lag[-1], 3)},
            "windows_lag_over_hop": sum(x > hop for x in lag),
            "max_arrival_gap_s": round(gap, 2), "valid": gap < 3 * hop}  # fmt: skip


def realtime(args: argparse.Namespace) -> None:
    require_clean_tree()
    require_ac_power()
    if not os.environ.get("LANDMARK_ALLOW_BATTERY"):
        wait_until_not_charging()  # charging draws from the CPU's adapter
    # Keep the machine (and display, so the lid-open requirement shows) awake
    # for this process's lifetime; a sleep mid-run freezes every timing.
    awake = subprocess.Popen(["caffeinate", "-dimsu", "-w", str(os.getpid())])
    out = args.out
    out.mkdir(parents=True, exist_ok=True)
    pool = mixes(args.mixes, args.sets)
    random.Random(args.seed).shuffle(pool)
    n = args.per_station
    stations = [{"id": f"st{i:03d}", "files": [str(ROOT / pool[(i * n + j) % len(pool)])
                                                for j in range(n)]}
                for i in range(args.stations)]  # fmt: skip
    (out / "stations.json").write_text(json.dumps(stations, indent=1))
    streams = [{"id": s["id"], "url": f"http://127.0.0.1:7950/stream/{s['id']}"} for s in stations]
    tmp = Path(tempfile.mkdtemp(prefix="live-rt-"))
    (tmp / "streams.json").write_text(json.dumps(streams))
    head = git_head()
    procs: list[subprocess.Popen] = []
    workers: list[subprocess.Popen] = []
    kill_unix = None
    with (
        cluster(
            B2_INDEX,
            1,
            args.deadline_ms,
            args.shard_workers,
            base_port=7100,
            stop_fraction=args.stop_fraction,
        ) as (b2, _),
        cluster(
            PAIR_INDEX,
            1,
            args.deadline_ms,
            args.shard_workers,
            base_port=7200,
            stop_fraction=args.stop_fraction,
        ) as (pairs, _),
    ):
        try:
            rlog = tmp / "registry.log"
            procs.append(subprocess.Popen(
                [str(BIN / "registry"), "-dir", str(tmp / "registry"), "-streams",
                 str(tmp / "streams.json"), "-listen", "127.0.0.1:7900", "-admin", "127.0.0.1:7901",
                 "-ttl", args.ttl],
                stdout=rlog.open("w"), stderr=subprocess.STDOUT))  # fmt: skip
            wait_for(rlog, "serving")
            start = time.time() + args.lead_s
            slog = tmp / "streamserver.log"
            procs.append(subprocess.Popen(
                [str(BIN / "streamserver"), "-stations", str(out / "stations.json"),
                 "-schedule", str(out / "schedule.jsonl"), "-listen", "127.0.0.1:7950",
                 "-start", f"{start:.6f}"],
                stdout=slog.open("w"), stderr=subprocess.STDOUT))  # fmt: skip
            wait_for(slog, "broadcasting")
            # One worker at a time, each once the previous holds its streams, so
            # the assignment is known: w0 fills first (the one --kill-at kills),
            # later workers take the rest or stand by.
            free = len(stations)
            for i in range(args.workers):
                log = tmp / f"monitor-w{i}.log"
                workers.append(subprocess.Popen(
                    [str(BIN / "monitor"), "-registry", "127.0.0.1:7900", "-id", f"w{i}",
                     "-capacity", str(args.capacity), "-heartbeat", args.heartbeat,
                     "-coordinator", b2, "-index", str(B2_INDEX),
                     "-pairs-coordinator", pairs, "-pairs-index", str(PAIR_INDEX),
                     "-window-log", str(out / f"windows-w{i}.jsonl"),
                     "-admin", f"127.0.0.1:{7910 + i}"],
                    stdout=log.open("w"), stderr=subprocess.STDOUT))  # fmt: skip
                want, until = min(args.capacity, free), time.monotonic() + 30
                while want and log.read_text(errors="replace").count("stream start") < want:
                    if time.monotonic() > until:
                        raise SystemExit(f"worker w{i} did not take {want} streams; see {tmp}")
                    time.sleep(0.2)
                free -= want
            if args.kill_at > 0:
                time.sleep(max(0.0, start + args.kill_at - time.time()))
                workers[0].kill()  # SIGKILL: no goodbye, leases must expire
                kill_unix = time.time()
            longest = max(len(s["files"]) for s in stations) * 110 + args.lead_s
            deadline = time.monotonic() + longest + 180
            power_problems: set[str] = set()
            while True:
                source, pct, state = battery()
                if source != "ac":
                    power_problems.add("on battery")
                if state in CHARGING:
                    power_problems.add(f"battery charging ({pct}%)")
                ended = sum((tmp / f"monitor-w{i}.log").read_text(errors="replace")
                            .count("stream source ended") for i in range(args.workers))  # fmt: skip
                if ended >= len(stations):
                    break
                if time.monotonic() > deadline:
                    raise SystemExit(
                        f"realtime run stopped: {ended}/{len(stations)} ended; see {tmp}"
                    )
                time.sleep(5)
            stop_unix = time.time()
            metrics = urllib.request.urlopen("http://127.0.0.1:7901/metrics").read().decode()
            (out / "registry_metrics.txt").write_text(
                "".join(x + "\n" for x in metrics.splitlines() if x.startswith("landmark_registry"))
            )
        finally:
            for p in workers + procs:
                if p.poll() is None:
                    p.send_signal(signal.SIGTERM)
            for p in workers + procs:
                p.wait(timeout=30)
    shutil.copytree(tmp / "registry" / "events", out / "events", dirs_exist_ok=True)
    for i in range(args.workers):
        shutil.copy(tmp / f"monitor-w{i}.log", out / f"monitor-w{i}.log")
    meta = {"mixes": args.mixes, "catalog": str((B2_INDEX / "catalog.json").relative_to(ROOT)),
            "stations": args.stations, "per_station": args.per_station, "sets": args.sets,
            "seed": args.seed, "workers": args.workers, "capacity": args.capacity,
            "heartbeat": args.heartbeat, "ttl": args.ttl, "broadcast_start_unix": start,
            "stop_fraction": args.stop_fraction,
            "kill_unix": kill_unix, "kill_at_s": args.kill_at or None, "stop_unix": stop_unix,
            **head, "power": run_out("pmset", "-g", "batt").splitlines()[:2]}  # fmt: skip
    (out / "run.json").write_text(json.dumps(meta, indent=1) + "\n")
    result = {
        **live_score.score(out),
        **lag_stats([out / f"windows-w{i}.jsonl" for i in range(args.workers)], 2.5),
        "power_problems": sorted(power_problems),
    }
    allowed = os.environ.get("LANDMARK_ALLOW_BATTERY")
    result["valid"] = result.get("valid", False) and (not power_problems or bool(allowed))
    (out / "result.json").write_text(json.dumps(result, indent=1) + "\n")
    print(json.dumps(result, indent=1))
    awake.terminate()
    if not result["valid"]:
        raise SystemExit("timings invalid: see power_problems and max_arrival_gap_s")


def main() -> None:
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    eq = sub.add_parser("equiv", help="live output equals offline output")
    eq.add_argument("--mixes", default="unmixdb_s8")
    eq.add_argument("--sets", nargs="+", default=["set044"])
    eq.add_argument("--out", type=Path, required=True)
    eq.add_argument("--capacity", type=int, default=8)
    eq.add_argument("--parallel", type=int, default=2, help="offline tracklist processes at once")
    eq.add_argument("--workers", type=int, default=8, help="matcher pool per shard")
    eq.add_argument("--deadline-ms", type=int, default=10000)
    eq.add_argument("--timeout", default="10s", help="per-search deadline, both runs")
    eq.add_argument("--live-timeout-s", type=float, default=3600)
    eq.add_argument("--limit", type=int, default=0, help="first N mixes only (smoke test)")
    eq.add_argument(
        "--hop",
        type=float,
        default=2.5,
        help="window hop, s, both runs (2.4961452: 27,520 samples, throughput T1 (a))",
    )
    eq.add_argument(
        "--stop-fraction",
        type=float,
        default=0.04,
        help="stop list (D30); the hybrid's calibration used 0.04",
    )
    rt = sub.add_parser("realtime", help="real-time stations, optional worker kill")
    rt.add_argument("--mixes", default="unmixdb_s8")
    rt.add_argument("--sets", nargs="+", default=TEST_SETS)
    rt.add_argument("--stations", type=int, required=True)
    rt.add_argument("--per-station", type=int, default=3, help="mixes per station, back to back")
    rt.add_argument("--workers", type=int, default=2)
    rt.add_argument("--capacity", type=int, required=True, help="streams per worker")
    rt.add_argument("--kill-at", type=float, default=0, help="kill -9 worker 0 this many s in")
    rt.add_argument("--heartbeat", default="2s")
    rt.add_argument("--ttl", default="6s")
    rt.add_argument(
        "--lead-s", type=float, default=25, help="broadcast starts this long after launch"
    )
    rt.add_argument("--shard-workers", type=int, default=8)
    rt.add_argument("--deadline-ms", type=int, default=10000)
    rt.add_argument("--seed", type=int, default=0)
    rt.add_argument(
        "--stop-fraction",
        type=float,
        default=0.04,
        help="stop list (D30); the hybrid's calibration used 0.04",
    )
    rt.add_argument("--out", type=Path, required=True)
    args = ap.parse_args()
    os.environ.setdefault("PYTHONHASHSEED", "0")
    {"equiv": equiv, "realtime": realtime}[args.cmd](args)


if __name__ == "__main__":
    main()
