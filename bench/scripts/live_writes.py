"""Add songs to a serving cluster under load (D42, docs/design/live-writes.md).

  python -m bench.scripts.live_writes --batches 10 --batch-size 100 --rate 200 \\
      --out bench/results/livewrites_r200

A copy of the fma_medium pair index (an APFS clone: no extra disk) is
served by two replicas of its one partition, each following the index's
manifests (shard -index), behind a coordinator, while bin/loadgen offers a
fixed rate of test queries. During the run, batches of held-out fma_medium
tracks (val and test negatives, never indexed) are added with ingest -add,
then the deltas are merged with ingest -merge. Measured:

- time from starting each add until a 10 s excerpt of one of its songs is
  identified through the coordinator (polling), and until the version is
  published;
- query latency and errors per second, in baseline, during adds, and during
  the merge (loadgen's per-request log);
- after the run, accuracy on 10 s excerpts of every added song against the
  same number of songs indexed from the start;
- mean postings scanned per search before and after the adds (the stop
  list is frozen, D42 W3).

Needs AC power with the battery not charging; the run is marked invalid
otherwise (or if the machine slept).
"""

import argparse
import gzip
import json
import shutil
import subprocess
import tempfile
import time
from pathlib import Path

import numpy as np

from bench.scripts.benchcluster import (
    BIN,
    CHARGING,
    ROOT,
    battery,
    require_ac_power,
    require_clean_tree,
    wait_for,
    wait_until_not_charging,
)

INDEX = ROOT / "data/segments/medium_unmixdb"
QUERIES = ROOT / "data/queries/medium_test_10s_seed0.gob"
MANIFEST = ROOT / "eval/manifests/splits_medium_seed0.json"
WORK = ROOT / "data/livewrites"


def audio(fma_id: int) -> Path:
    t = f"{fma_id:06d}"
    return ROOT / "data/fma_medium" / t[:3] / f"{t}.mp3"


def excerpt(src: Path, dst: Path, start: float = 5.0) -> None:
    subprocess.run(["ffmpeg", "-nostdin", "-v", "error", "-y", "-ss", str(start), "-t", "10",
                    "-i", str(src), "-ac", "1", "-ar", "22050", str(dst)], check=True)  # fmt: skip


def scrape(port: int, name: str) -> float:
    """A Prometheus sample's value from a local admin port (0 if absent)."""
    import urllib.request

    text = urllib.request.urlopen(f"http://127.0.0.1:{port}/metrics").read().decode()
    return sum(float(x.split()[-1]) for x in text.splitlines() if x.startswith(name + " ") or
               x.startswith(name + "{"))  # fmt: skip


def postings_per_search(admin_ports: list[int]) -> list[float]:
    """Each shard's mean postings scanned per search so far."""
    name = "landmark_shard_postings_scanned"
    return [scrape(a, name + "_sum") / max(scrape(a, name + "_count"), 1) for a in admin_ports]


def probe(files: list[Path], coord: str, idx: Path, out: Path) -> list[dict]:
    """Identify files through the coordinator with bench/h2hquery."""
    lst = out.with_suffix(".txt")
    lst.write_text("".join(f"{f}\n" for f in files))
    subprocess.run([str(BIN / "h2hquery"), "-list", str(lst), "-index", str(idx),
                    "-pairs-coordinator", coord, "-workers", "4", "-out", str(out)],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)  # fmt: skip
    import csv

    with out.open() as fh:
        return list(csv.DictReader(fh))


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--batches", type=int, default=10)
    ap.add_argument("--batch-size", type=int, default=100)
    ap.add_argument("--interval-s", type=float, default=30, help="time between the starts of adds")
    ap.add_argument("--rate", type=float, default=200, help="offered queries per second")
    ap.add_argument("--ingest-workers", type=int, default=4)
    ap.add_argument("--dry-run", action="store_true", help="skip the power checks; results invalid")
    ap.add_argument("--out", type=Path, required=True)
    args = ap.parse_args()
    if not args.dry_run:
        require_clean_tree()
        require_ac_power()
        wait_until_not_charging()
    out = args.out
    out.mkdir(parents=True, exist_ok=True)
    m = json.loads(MANIFEST.read_text())
    held = sorted(int(f) for f in m["val_neg"] + m["test_neg"])[: args.batches * args.batch_size]
    originals = sorted(int(f) for f in m["test_pos"])[: len(held)]

    if WORK.exists():
        shutil.rmtree(WORK)
    WORK.mkdir(parents=True)
    idx = WORK / "idx"
    subprocess.run(["cp", "-cR", str(INDEX), str(idx)], check=True)  # clone: shares blocks
    for f in idx.glob("manifest-*.json"):
        f.unlink()
    ex = WORK / "excerpts"
    ex.mkdir()
    for f in held + originals:
        excerpt(audio(f), ex / f"{f:06d}.wav")
    batches = [held[i : i + args.batch_size] for i in range(0, len(held), args.batch_size)]
    for k, b in enumerate(batches):
        (WORK / f"add{k:02d}.tsv").write_text("".join(f"{f}\t{audio(f)}\n" for f in b))

    tmp = Path(tempfile.mkdtemp(prefix="livewrites-"))
    procs: list[subprocess.Popen] = []
    shard_admin = [9411, 9412]
    timeline: dict = {"adds": [], "merge": None}
    t_wall0, t_mono0, power0 = time.time(), time.monotonic(), battery()
    load = None
    try:
        replicas = []
        for k, admin in enumerate(shard_admin):
            addr = f"127.0.0.1:{8411 + k}"
            log = tmp / f"shard{k}.log"
            procs.append(subprocess.Popen(
                [str(BIN / "shard"), "-index", str(idx), "-partition", "0", "-poll", "500ms",
                 "-listen", addr, "-admin", f"127.0.0.1:{admin}", "-workers", "6"],
                stdout=log.open("w"), stderr=subprocess.STDOUT))  # fmt: skip
            wait_for(log, "serving", 120)
            replicas.append(addr)
        cfg = {"index_dir": str(idx), "deadline_ms": 1000, "t_abs": 30, "t_ratio": 1.0,
               "stop_fraction": 0.04, "partitions": [{"id": 0, "replicas": replicas}]}  # fmt: skip
        (tmp / "cluster.json").write_text(json.dumps(cfg))
        coord = "127.0.0.1:8421"
        clog = tmp / "coordinator.log"
        cmd = [str(BIN / "coordinator"), "-config", str(tmp / "cluster.json"), "-listen", coord,
               "-admin", "127.0.0.1:9421"]  # fmt: skip
        procs.append(subprocess.Popen(cmd, stdout=clog.open("w"), stderr=subprocess.STDOUT))
        wait_for(clog, "serving")

        baseline_s = 30
        duration = baseline_s + len(batches) * args.interval_s + 120
        cmd = [str(BIN / "loadgen"), "run", "-target", coord, "-queries", str(QUERIES),
               "-rate", str(args.rate), "-warmup", "10s", "-duration", f"{duration:.0f}s",
               "-out", str(out / "loadgen"), "-label", "live writes"]  # fmt: skip
        load = subprocess.Popen(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        time.sleep(10 + baseline_s)  # warmup, then a baseline
        postings0 = postings_per_search(shard_admin)

        for k, b in enumerate(batches):
            t0 = time.time()
            cmd = [str(BIN / "ingest"), "-add", str(WORK / f"add{k:02d}.tsv"), "-out", str(idx),
                   "-workers", str(args.ingest_workers)]  # fmt: skip
            subprocess.run(cmd, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            t_pub = time.time()
            target = ex / f"{b[0]:06d}.wav"
            found = None
            while time.time() - t0 < 60:
                r = probe([target], coord, idx, tmp / "probe.csv")[0]
                if r["top1"] == f"ref:{b[0]}":
                    found = time.time()
                    break
                time.sleep(0.1)
            add = {"batch": k, "songs": len(b), "start_unix": t0, "published_unix": t_pub,
                   "identified_unix": found}  # fmt: skip
            timeline["adds"].append(add)
            seen = (found - t0) if found else float("nan")
            print(f"batch {k}: published {t_pub - t0:.1f} s, identified {seen:.1f} s after start",
                  flush=True)  # fmt: skip
            time.sleep(max(0.0, t0 + args.interval_s - time.time()))

        t0 = time.time()
        subprocess.run([str(BIN / "ingest"), "-merge", "-out", str(idx)], check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)  # fmt: skip
        timeline["merge"] = {"start_unix": t0, "published_unix": time.time()}
        print(f"merge published in {time.time() - t0:.1f} s", flush=True)
        load.wait(timeout=duration + 120)
        postings1 = postings_per_search(shard_admin)

        added = probe([ex / f"{f:06d}.wav" for f in held], coord, idx, tmp / "added.csv")
        orig = probe([ex / f"{f:06d}.wav" for f in originals], coord, idx, tmp / "orig.csv")
    finally:
        if load is not None and load.poll() is None:
            load.terminate()
        for p in procs:
            if p.poll() is None:
                p.terminate()
        for p in procs:
            p.wait(timeout=30)
        for log in tmp.glob("*.log"):
            shutil.copy(log, out / log.name)
    slept = (time.time() - t_wall0) - (time.monotonic() - t_mono0)
    power1 = battery()

    # Latency by phase from loadgen's request log (due times count from start_unix).
    start_unix = json.loads((out / "loadgen" / "summary.json").read_text())["start_unix"]
    rows = []
    with gzip.open(out / "loadgen" / "requests.csv.gz", "rt") as fh:
        header = fh.readline().strip().split(",")
        for line in fh:
            rows.append(dict(zip(header, line.strip().split(","), strict=True)))

    def phase(t: float) -> str:
        if (
            timeline["merge"]
            and timeline["merge"]["start_unix"] <= t <= timeline["merge"]["published_unix"] + 5
        ):
            return "merge"
        for a in timeline["adds"]:
            if a["start_unix"] <= t <= (a["identified_unix"] or a["published_unix"]) + 5:
                return "adding"
        return "baseline" if t < timeline["adds"][0]["start_unix"] else "between"

    lat: dict[str, list[float]] = {}
    errors: dict[str, int] = {}
    for r in rows:
        ph = phase(start_unix + float(r["due_ms"]) / 1000)
        if r["code"] != "OK":
            errors[ph] = errors.get(ph, 0) + 1
            continue
        lat.setdefault(ph, []).append(float(r["latency_ms"]))

    def stats(v: list[float]) -> dict:
        a = np.array(v)
        return {"n": len(a), "p50_ms": round(float(np.percentile(a, 50)), 2),
                "p99_ms": round(float(np.percentile(a, 99)), 2)} if len(a) else {}  # fmt: skip

    def accuracy(rs: list[dict], ids: list[int]) -> float:
        want = {f"{f:06d}.wav": f for f in ids}
        ok = 0
        for r in rs:
            f = want[Path(r["file"]).name]
            ok += r["top1"] in (f"ref:{f}", f"fma:{f}")
        return round(ok / len(rs), 4)

    adds = timeline["adds"]
    result = {
        "batches": len(batches), "batch_size": args.batch_size, "rate_qps": args.rate,
        "publish_s": [round(a["published_unix"] - a["start_unix"], 2) for a in adds],
        "identifiable_s": [round(a["identified_unix"] - a["start_unix"], 2)
                           if a["identified_unix"] else None for a in adds],
        "identifiable_after_publish_s": [round(a["identified_unix"] - a["published_unix"], 2)
                                         if a["identified_unix"] else None for a in adds],
        "merge_s": round(timeline["merge"]["published_unix"] - timeline["merge"]["start_unix"], 2),
        "latency": {ph: stats(v) for ph, v in lat.items()}, "errors": errors,
        "accuracy_added": accuracy(added, held), "accuracy_original": accuracy(orig, originals),
        "postings_per_search_before": [round(x) for x in postings0],
        "postings_per_search_after": [round(x) for x in postings1],
        "machine_slept_s": round(slept, 1), "power_before": power0, "power_after": power1,
        "valid": not args.dry_run and slept < 2 and power1[0] == "ac" and power1[2] not in CHARGING,
    }  # fmt: skip
    (out / "result.json").write_text(json.dumps(result, indent=1) + "\n")
    (out / "timeline.json").write_text(json.dumps(timeline, indent=1) + "\n")
    print(json.dumps(result, indent=1))
    shutil.rmtree(WORK)


if __name__ == "__main__":
    main()
