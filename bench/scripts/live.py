"""Run live monitoring end to end on local processes (D38-D40).

  # Live output equals offline output, mix for mix:
  python -m bench.scripts.live equiv --sets set044 --out eval/results/live/equiv_set044

equiv starts the triplet and pair clusters (benchcluster), runs
bin/tracklist -json on every mix of the sets (offline), then runs the
registry and one bin/monitor worker with every mix as a file stream read as
fast as possible, and compares each stream's logged entries with the
offline ones. Writes offline_runs.jsonl and live_runs.jsonl (eval.score_cli
format), the registry's event logs, and result.json.
"""

import argparse
import concurrent.futures
import json
import os
import shutil
import signal
import subprocess
import tempfile
import time
from pathlib import Path

from bench.scripts.benchcluster import BIN, ROOT, cluster, wait_for

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


def offline(files: list[str], b2: str, pairs: str, parallel: int, timeout: str) -> dict[str, list]:
    def one(f: str) -> tuple[str, list]:
        out = subprocess.run(
            [str(BIN / "tracklist"), "-coordinator", b2, "-index", str(B2_INDEX),
             "-pairs-coordinator", pairs, "-pairs-index", str(PAIR_INDEX), "-timeout", timeout,
             "-json", str(ROOT / f)],
            capture_output=True, text=True, check=True,
        ).stdout  # fmt: skip
        return f, json.loads(out)

    with concurrent.futures.ThreadPoolExecutor(parallel) as ex:
        return dict(ex.map(one, files))


def live(files: list[str], b2: str, pairs: str, out: Path, capacity: int, timeout: str,
         timeout_s: float) -> dict[str, list]:  # fmt: skip
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
             "-capacity", str(capacity), "-read-rate", "0", "-timeout", timeout,
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
        cluster(B2_INDEX, 1, args.deadline_ms, args.workers, base_port=7100) as (b2, _),
        cluster(PAIR_INDEX, 1, args.deadline_ms, args.workers, base_port=7200) as (pairs, _),
    ):
        t0 = time.monotonic()
        off = offline(files, b2, pairs, args.parallel, args.timeout)
        t_off = time.monotonic() - t0
        t0 = time.monotonic()
        on = live(files, b2, pairs, out, args.capacity, args.timeout, args.live_timeout_s)
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
        "capacity": args.capacity, "parallel_offline": args.parallel, **head,
        "power": run_out("pmset", "-g", "batt").splitlines()[:1],
    }  # fmt: skip
    (out / "result.json").write_text(json.dumps(result, indent=1) + "\n")
    print(json.dumps({k: v for k, v in result.items() if k != "differing"}, indent=1))


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
    args = ap.parse_args()
    os.environ.setdefault("PYTHONHASHSEED", "0")
    {"equiv": equiv}[args.cmd](args)


if __name__ == "__main__":
    main()
