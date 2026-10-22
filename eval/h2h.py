"""Head-to-head against audfprint and Panako (D41, docs/design/head-to-head.md).

  python -m eval.h2h refs                          # the reference list every system indexes
  python -m eval.h2h index-audfprint --density 20  # build a database, timed
  python -m eval.h2h index-panako --time-filter 25
  python -m eval.h2h clips --split val --systems landmark audfprint:20 panako:25
  python -m eval.h2h score --systems landmark_pairs landmark_b2 audfprint:20 panako:25

Every system indexes the tracks Landmark's fma_medium + UnmixDB index holds
(its catalog.json and extras.json) and answers the same query files:
10 s excerpts of the split's positives and negatives (one each), degraded
per condition and written at 22,050 Hz, so each tool decodes at its own
rate (Landmark and audfprint 11,025 Hz; Panako 16,000 Hz). Queries are
made one condition at a time and deleted after every system has answered
them (all conditions at once would need about 13 GB per split).

Scoring: a positive is correct when the top answer is in the query's
duplicate cluster (D12); each system's accept rule (top score, and top over
the best score outside the top's cluster, as D13 and D14) is calibrated on
val to a pooled false-accept rate of at most 0.1%, maximizing mean accuracy
over all conditions, the same objective for every system; test is
reported with Wilson intervals.

Tools live under data/tools (D41); databases under data/h2h.
"""

import argparse
import json
import os
import re
import resource
import shutil
import subprocess
import time
from collections import defaultdict
from multiprocessing import Pool
from pathlib import Path

import numpy as np
import pandas as pd
import soundfile as sf

from eval.backends import dense_tables
from eval.degrade import excerpt
from eval.run_eval import condition
from eval.stats import wilson
from prototype.audio import decode, encode_decode_mp3
from prototype.fma import audio_path

ROOT = Path(__file__).resolve().parent.parent
WORK = ROOT / "data" / "h2h"
RES = ROOT / "eval" / "results" / "h2h"
TOOLS = ROOT / "data" / "tools"
INDEX = ROOT / "data" / "segments" / "medium_unmixdb"
B2_INDEX = ROOT / "data" / "segments" / "medium_unmixdb_b2"
MANIFEST = ROOT / "eval" / "manifests" / "splits_medium_seed0.json"
RATE = 22_050
SECONDS = 10.0
SEED = 0
CONDITIONS = [
    "clean_10s", "white_snr10_10s", "white_snr0_10s", "mp3_64k_10s",
    "speed_+2pct_10s", "speed_-5pct_10s", "speed_+8pct_10s",
    "keylock_-2pct_10s", "keylock_+5pct_10s", "keylock_-8pct_10s",
]  # fmt: skip
T_RATIO = [1.0, 1.25, 1.5, 2.0, 2.5, 3.0, 4.0]
MAX_FAR = 0.001
JAVA = Path("/opt/homebrew/opt/openjdk@17/bin/java")
PANAKO_JAR = TOOLS / "Panako" / "build" / "libs" / "panako-2.1-all.jar"
AUDFPRINT = TOOLS / "audfprint" / "audfprint.py"
AUDFPRINT_PY = TOOLS / "audfprint-venv" / "bin" / "python"


# References ---------------------------------------------------------------


def refs(_: argparse.Namespace) -> None:
    """data/h2h/refs.tsv: path<TAB>id for every track Landmark indexed, with
    ids as h2hquery writes them (fma:<id>, ref:<name>)."""
    cat = json.loads((INDEX / "catalog.json").read_text())
    extras = {e["fma_id"]: e for e in json.loads((INDEX / "extras.json").read_text())}
    WORK.mkdir(parents=True, exist_ok=True)
    lines = []
    for t in cat:
        if t["fma_id"] in extras:
            e = extras[t["fma_id"]]
            lines.append(f"{e['path']}\tref:{e['name']}")
        else:
            lines.append(f"{audio_path(t['fma_id'], 'medium').resolve()}\tfma:{t['fma_id']}")
    (WORK / "refs.tsv").write_text("\n".join(lines) + "\n")
    (WORK / "refs.txt").write_text("".join(x.split("\t")[0] + "\n" for x in lines))
    print(f"{len(lines)} references")


def ref_ids() -> dict[str, str]:
    out = {}
    for line in (WORK / "refs.tsv").read_text().splitlines():
        path, rid = line.split("\t")
        out[path] = rid
    return out


def clusters() -> dict[str, str]:
    """Track id -> duplicate-cluster id (D12); references are their own."""
    fma, cluster = dense_tables(json.loads(MANIFEST.read_text()))
    return {f"fma:{fma[d]}": f"fma:{fma[cluster[d]]}" for d in range(len(fma))}


# Indexing -----------------------------------------------------------------


def timed(cmd: list[str], log: Path, env: dict | None = None) -> dict:
    """Run cmd, logging its output; wall and CPU seconds of it and its children."""
    before = resource.getrusage(resource.RUSAGE_CHILDREN)
    t = time.monotonic()
    with log.open("w") as fh:
        subprocess.run(cmd, stdout=fh, stderr=subprocess.STDOUT, check=True, env=env)
    after = resource.getrusage(resource.RUSAGE_CHILDREN)
    cpu = (after.ru_utime - before.ru_utime) + (after.ru_stime - before.ru_stime)
    return {"wall_s": round(time.monotonic() - t, 1), "cpu_s": round(cpu, 1)}


def du(path: Path) -> int:
    return sum(f.stat().st_size for f in path.rglob("*") if f.is_file()) if path.is_dir() else (
        path.stat().st_size)  # fmt: skip


def power() -> list[str]:
    return subprocess.run(
        ["pmset", "-g", "batt"], capture_output=True, text=True
    ).stdout.splitlines()[:2]


def record_cost(name: str, cost: dict) -> None:
    RES.mkdir(parents=True, exist_ok=True)
    path = RES / "index_costs.json"
    costs = json.loads(path.read_text()) if path.exists() else {}
    costs[name] = {**cost, "power": power()}
    path.write_text(json.dumps(costs, indent=1) + "\n")
    print(name, costs[name])


def audfprint_db(density: float) -> Path:
    return WORK / "dbs" / f"audfprint_d{density:g}.pklz"


def index_audfprint(args: argparse.Namespace) -> None:
    db = audfprint_db(args.density)
    db.parent.mkdir(parents=True, exist_ok=True)
    cost = timed([str(AUDFPRINT_PY), str(AUDFPRINT), "new", "--dbase", str(db),
                  "--density", str(args.density), "--ncores", str(args.cores),
                  "--list", str(WORK / "refs.txt")],
                 WORK / f"index_audfprint_d{args.density:g}.log")  # fmt: skip
    record_cost(f"audfprint:{args.density:g}", {**cost, "bytes": du(db), "cores": args.cores})


def panako_conf(time_filter: int, cores: int) -> list[str]:
    db = WORK / "dbs" / f"panako_t{time_filter}"
    return ["STRATEGY=PANAKO", f"PANAKO_LMDB_FOLDER={db}", f"PANAKO_CACHE_FOLDER={db}_cache",
            "PANAKO_CACHE_TO_FILE=FALSE", "PANAKO_USE_CACHED_PRINTS=FALSE",
            f"PANAKO_TIME_MAX_FILTER_SIZE={time_filter}", f"AVAILABLE_PROCESSORS={cores}",
            "CHECK_FOR_DUPLICATES=FALSE"]  # fmt: skip


def panako(*args: str) -> list[str]:
    return [str(JAVA), "--add-opens=java.base/java.nio=ALL-UNNAMED",
            "-Dlmdbjava.native.lib=/opt/homebrew/lib/liblmdb.dylib", "-jar", str(PANAKO_JAR),
            *args]  # fmt: skip


def index_panako(args: argparse.Namespace) -> None:
    """--list resumes a build with the files not yet stored (Panako's native
    transform crashed once under 12 threads); only a full build's cost is
    recorded."""
    conf = panako_conf(args.time_filter, args.cores)
    db = Path(conf[1].split("=", 1)[1])
    db.mkdir(parents=True, exist_ok=True)
    files = args.list or WORK / "refs.txt"
    suffix = "" if args.list is None else "_resume"
    cost = timed(panako("store", *conf, str(files)),
                 WORK / f"index_panako_t{args.time_filter}{suffix}.log")  # fmt: skip
    if args.list is None:
        record_cost(f"panako:{args.time_filter}", {**cost, "bytes": du(db), "cores": args.cores})


# Queries ------------------------------------------------------------------


def _make(job: tuple[int, str, str, int, Path]) -> dict:
    fma_id, label, cond, ci, out = job
    row = {"fma_id": fma_id, "label": label, "cond": cond, "file": str(out)}
    try:
        pcm = decode(audio_path(fma_id, "medium"), RATE)
        secs, degrade = condition(cond)
        clip, start = excerpt(
            pcm, secs, np.random.default_rng([SEED, fma_id, 0, int(secs * 1000)]), RATE
        )
        rng = np.random.default_rng([SEED, fma_id, 0, ci])
        if cond.startswith("mp3_"):
            q = encode_decode_mp3(clip, cond.split("_")[1], RATE)
        elif cond.startswith("keylock_"):  # WSOLA frames in seconds as at 11,025 Hz
            from eval.degrade import keylock

            factor = 1 + float(re.search(r"([+-][\d.]+)pct", cond).group(1)) / 100
            q = keylock(clip, factor, frame=2048, tolerance=512)
        else:
            q = degrade(clip, rng)
        sf.write(out, q.astype(np.float32), RATE, subtype="FLOAT")
        row["start_s"] = start / RATE
    except Exception as e:  # noqa: BLE001 (recorded per query, like run_eval)
        row["error"] = str(e)
    return row


def make_queries(split: str, cond: str, workers: int) -> tuple[Path, list[dict]]:
    m = json.loads(MANIFEST.read_text())
    tracks = [(int(f), "pos") for f in m[f"{split}_pos"]] + [
        (int(f), "neg") for f in m[f"{split}_neg"]
    ]
    d = WORK / "q" / split / cond
    if d.exists():
        shutil.rmtree(d)
    d.mkdir(parents=True)
    ci = CONDITIONS.index(cond)
    jobs = [(f, label, cond, ci, d / f"{label}_{f:06d}.wav") for f, label in tracks]
    with Pool(workers) as pool:
        rows = pool.map(_make, jobs, chunksize=8)
    (d / "list.txt").write_text("".join(r["file"] + "\n" for r in rows if "error" not in r))
    return d, rows


# Answering ----------------------------------------------------------------


def run_landmark(d: Path, workers: int, pairs: str, b2: str) -> pd.DataFrame:
    out = d / "landmark.csv"
    subprocess.run([str(ROOT / "bin" / "h2hquery"), "-list", str(d / "list.txt"),
                    "-index", str(INDEX),
                    "-pairs-coordinator", pairs, "-b2-coordinator", b2, "-workers", str(workers),
                    "-out", str(out)], check=True)  # fmt: skip
    df = pd.read_csv(out, keep_default_na=False)
    return df.rename(columns={"top1_score": "score", "runner_up_score": "runner_up"})


# A hit: (score, track id, tempo factor, reference time at query time 0).
Hit = tuple[int, str, float, float]

_AFP = re.compile(
    r"^Matched\s+[\d.]+ s starting at\s+([\d.]+) s in (.+?) to time\s+([\d.]+) s in (.+?)"
    r" with\s+(\d+) of\s+\d+ common hashes at rank\s+\d+"
)
_AFP_NOMATCH = re.compile(r"^NOMATCH (.+?) [\d.]+ sec")


def audfprint_hits(files: Path, density: float, cores: int, ids: dict) -> dict[str, list[Hit]]:
    db = audfprint_db(density)
    proc = subprocess.run([str(AUDFPRINT_PY), str(AUDFPRINT), "match", "--dbase", str(db),
                           "--density", str(density), "--max-matches", "5", "--find-time-range",
                           "--ncores", str(cores), "--list", str(files)],
                          capture_output=True, text=True, check=True)  # fmt: skip
    hits: dict[str, list[Hit]] = defaultdict(list)
    for line in proc.stdout.splitlines():
        if m := _AFP.match(line):
            q_at, query, r_at, ref, n = m.groups()
            rid = ids.get(ref, "unknown:" + ref)
            hits[query].append((int(n), rid, 1.0, float(r_at) - float(q_at)))
        elif m := _AFP_NOMATCH.match(line):
            hits.setdefault(m.group(1), [])
    return hits


def panako_ids(time_filter: int, ids: dict) -> dict[str, str]:
    """Panako's resource id -> track id, from its store logs. Panako answers
    some matches with "metadata unavailable!" instead of the path (path
    records lost when its native transform crashed mid-build, while the
    fingerprints were kept); the id it still reports names the file: the
    number for numeric file names (every FMA track), else a content hash."""
    out = {}
    for log in WORK.glob(f"index_panako_t{time_filter}*.log"):
        for m in re.finditer(r"Stored \d+ fingerprints for '(.+?)', id: (-?\d+)", log.read_text()):
            out[m.group(2)] = ids.get(m.group(1), "unknown:" + m.group(1))
    return out


def panako_hits(files: Path, time_filter: int, cores: int, ids: dict) -> dict[str, list[Hit]]:
    """Columns: index; total; query; q start; q stop; match path; match id;
    m start; m stop; score; time factor; frequency factor; seconds with match."""
    paths = files.read_text().split()
    by_id = panako_ids(time_filter, ids)
    fma = {v for v in ids.values() if v.startswith("fma:")}
    hits: dict[str, list[Hit]] = defaultdict(list)
    conf = panako_conf(time_filter, cores)
    for i in range(0, len(paths), 250):
        batch = paths[i : i + 250]
        proc = subprocess.run(panako("query", *conf, "NUMBER_OF_QUERY_RESULTS=5", *batch),
                              capture_output=True, text=True, check=True)  # fmt: skip
        for line in proc.stdout.splitlines():
            c = [x.strip().rstrip("%").strip() for x in line.split(";")]
            if len(c) >= 13 and c[0].isdigit() and c[5] not in ("", "null"):
                factor = float(c[10])
                rid = ids.get(c[5]) or by_id.get(c[6])
                if rid is None:
                    rid = f"fma:{c[6]}" if f"fma:{c[6]}" in fma else "unknown:" + c[5]
                hits[c[2]].append(
                    (int(float(c[9])), rid, factor, float(c[7]) - float(c[3]) * factor)
                )
    return hits


def answers(paths: list[str], hits: dict[str, list[Hit]], cl: dict) -> list[dict]:
    """Per query file: the top hit and the best score outside its cluster."""
    rows = []
    for f in paths:
        h = sorted(hits.get(f, []), key=lambda x: -x[0])
        top = h[0] if h else (0, "", 1.0, 0.0)
        second = next((x[0] for x in h[1:] if cl.get(x[1], x[1]) != cl.get(top[1], top[1])), 0)
        rows.append({"file": f, "top1": top[1], "score": top[0], "runner_up": second,
                     "factor": top[2], "ref_at_0": top[3]})  # fmt: skip
    return rows


def tool_rows(system: str, files: Path, cores: int, ids: dict, cl: dict) -> pd.DataFrame:
    name, _, knob = system.partition(":")
    if name == "audfprint":
        hits = audfprint_hits(files, float(knob), cores, ids)
    elif name == "panako":
        hits = panako_hits(files, int(knob), cores, ids)
    else:
        raise SystemExit(f"unknown system {system}")
    df = pd.DataFrame(answers(files.read_text().split(), hits, cl))
    df["system"] = system
    return df


def landmark(d: Path, workers: int) -> pd.DataFrame:
    """Both Landmark clusters, with the stop list at its deploy default (D30)."""
    from bench.scripts.benchcluster import cluster

    with (
        cluster(INDEX, 1, 10000, 8, base_port=8200, stop_fraction=0.04) as (pairs, _),
        cluster(B2_INDEX, 1, 10000, 8, base_port=8300, stop_fraction=0.04) as (b2, _),
    ):
        return run_landmark(d, workers, pairs, b2)


def clips(args: argparse.Namespace) -> None:
    ids, cl = ref_ids(), clusters()
    RES.mkdir(parents=True, exist_ok=True)
    for cond in args.conds:
        d, made = make_queries(args.split, cond, args.workers)
        meta = pd.DataFrame(made)
        parts = []
        for system in args.systems:
            if system == "landmark":
                parts.append(landmark(d, args.workers))
            else:
                parts.append(tool_rows(system, d / "list.txt", args.cores, ids, cl))
        meta = meta.drop(columns=["error"], errors="ignore")
        rows = pd.concat(parts).merge(meta, on="file", how="left")
        rows["file"] = rows["file"].map(lambda p: Path(p).name)
        out = RES / f"rows_{args.split}_{cond}.csv.gz"
        if out.exists():  # add or replace these systems' rows
            old = pd.read_csv(out, keep_default_na=False)
            rows = pd.concat([old[~old["system"].isin(rows["system"].unique())], rows])
        rows.to_csv(out, index=False)
        shutil.rmtree(d)
        print(cond, rows.groupby("system").size().to_dict(), flush=True)


# DJ mixes -----------------------------------------------------------------

MIXES = "unmixdb_s8"
WINDOW_S, HOP_S = 10.0, 2.5
FRAMES_PER_S = 11025 / 256  # Landmark's offset unit, which the align decoder reads


def mix_mp3(m: dict) -> Path:
    """The published mix (44.1 kHz MP3), not the 11,025 Hz WAV the eval made."""
    return (
        ROOT
        / "data/unmixdb/extracted"
        / f"mixotic-{m['set']}-excerpts"
        / "mixes"
        / f"{m['name']}.mp3"
    )


def _cut(job: tuple[int, Path, Path]) -> list[dict]:
    mix, path, d = job
    pcm = decode(path, RATE)
    w, hop = int(WINDOW_S * RATE), int(HOP_S * RATE)
    out = []
    for i, a in enumerate(range(0, len(pcm) - w + 1, hop)):
        f = d / f"{mix:04d}_{i:03d}.wav"
        sf.write(f, pcm[a : a + w], RATE, subtype="FLOAT")
        out.append({"file": str(f), "mix": mix, "t0": round(a / RATE * 1000) / 1000})
    return out


def tag(system: str) -> str:
    return "_h2h_" + system.replace(":", "")


def mixes(args: argparse.Namespace) -> None:
    """Every window of every UnmixDB mix answered by each tool, written as
    eval/results/tracklist/windows_unmixdb_s8_h2h_<tool><knob>.csv.gz for
    eval.tracklist_score (one hypothesis per window, mode "none")."""
    gt = json.loads((ROOT / "eval" / "mixes" / f"{MIXES}.json").read_text())
    ids, cl = ref_ids(), clusters()
    dense = {rid: d for d, rid in enumerate(ids.values())}  # refs.tsv is in catalog order
    todo = [i for i, m in enumerate(gt["mixes"]) if not args.sets or m["set"] in args.sets]
    parts_dir = WORK / "mixparts"
    for b in range(0, len(todo), args.batch):
        batch = todo[b : b + args.batch]
        want = [s for s in args.systems if not (parts_dir / f"{tag(s)}_{b:05d}.csv").exists()]
        if not want:
            continue
        d = WORK / "w"
        if d.exists():
            shutil.rmtree(d)
        d.mkdir(parents=True)
        with Pool(args.workers) as pool:
            windows = [x for r in pool.map(_cut, [(i, mix_mp3(gt["mixes"][i]), d) for i in batch])
                       for x in r]  # fmt: skip
        (d / "list.txt").write_text("".join(w["file"] + "\n" for w in windows))
        meta = pd.DataFrame(windows)
        for system in want:
            r = tool_rows(system, d / "list.txt", args.cores, ids, cl).merge(meta, on="file")
            out = pd.DataFrame({
                "mix": r["mix"], "t0": r["t0"], "mode": "none", "factor": r["factor"],
                "top1_dense": r["top1"].map(lambda t: dense.get(t, -1)), "score": r["score"],
                "runner_up": r["runner_up"], "offset": (r["ref_at_0"] * FRAMES_PER_S).round(),
                "n_landmarks": 0,
            })  # fmt: skip
            parts_dir.mkdir(parents=True, exist_ok=True)
            out.to_csv(parts_dir / f"{tag(system)}_{b:05d}.csv", index=False)
        shutil.rmtree(d)
        print(f"mixes {b + len(batch)}/{len(todo)}", flush=True)
    res = ROOT / "eval" / "results" / "tracklist"
    for system in args.systems:
        parts = sorted(parts_dir.glob(f"{tag(system)}_*.csv"))
        w = pd.concat(pd.read_csv(p) for p in parts).sort_values(["mix", "t0"])
        w.to_csv(res / f"windows_{MIXES}{tag(system)}.csv.gz", index=False)
        (res / f"windows_{MIXES}{tag(system)}.meta.json").write_text(json.dumps(
            {"mixes": MIXES, "system": system, "window_s": WINDOW_S, "hop_s": HOP_S,
             "rate": RATE, "windows": len(w)}, indent=1) + "\n")  # fmt: skip


# Cost ---------------------------------------------------------------------


def cost(args: argparse.Namespace) -> None:
    """Index build on the same subset of references, and query time on one
    core, for every system; needs AC power, not charging (as benchmarks).
    Build cost is compared in CPU seconds per track (Panako runs on fewer
    threads: its native transform crashed under 12). Query time excludes
    loading the database or starting the JVM: each tool also answers a
    single query, and that run's wall time is subtracted."""
    from bench.scripts.benchcluster import battery, cluster, require_ac_power

    require_ac_power()
    out = WORK / "cost"
    if out.exists():
        shutil.rmtree(out)
    out.mkdir(parents=True)
    pairs = [x.split("\t") for x in (WORK / "refs.tsv").read_text().splitlines()[: args.tracks]]
    (out / "refs.txt").write_text("".join(path + "\n" for path, _ in pairs))
    (out / "extras.tsv").write_text("".join(f"{rid}\t{path}\n" for path, rid in pairs))
    result: dict = {"tracks": len(pairs), "power_before": battery()}
    cores = os.cpu_count() or 1
    builds = {}
    for kind in ("pairs", "triplets"):
        dest = out / f"landmark_{kind}"
        cmd = [str(ROOT / "bin" / "ingest"), "-manifest", "", "-extra", str(out / "extras.tsv"),
               "-fingerprint", kind, "-out", str(dest)]  # fmt: skip
        builds[f"landmark_{kind}"] = {**timed(cmd, out / f"ingest_{kind}.log"), "dest": dest,
                                      "cores": cores}  # fmt: skip
    afp = out / "audfprint.pklz"
    cmd = [str(AUDFPRINT_PY), str(AUDFPRINT), "new", "--dbase", str(afp), "--density",
           str(args.density), "--ncores", str(cores), "--list", str(out / "refs.txt")]  # fmt: skip
    builds[f"audfprint:{args.density:g}"] = {**timed(cmd, out / "audfprint_new.log"), "dest": afp,
                                             "cores": cores}  # fmt: skip
    conf = panako_conf(args.time_filter, args.panako_cores)
    conf[1] = f"PANAKO_LMDB_FOLDER={out / 'panako'}"
    conf[2] = f"PANAKO_CACHE_FOLDER={out / 'panako_cache'}"
    (out / "panako").mkdir()
    builds[f"panako:{args.time_filter}"] = {
        **timed(panako("store", *conf, str(out / "refs.txt")), out / "panako_store.log"),
        "dest": out / "panako", "cores": args.panako_cores,
    }  # fmt: skip
    for b in builds.values():
        b["bytes"] = du(b.pop("dest"))
        b["cpu_ms_per_track"] = round(1000 * b["cpu_s"] / len(pairs), 1)
        b["bytes_per_track"] = round(b["bytes"] / len(pairs))
    result["build"] = builds

    # Query time on one core: clean test positives against the full indexes.
    d, made = make_queries("test", "clean_10s", 8)
    files = [r["file"] for r in made if "error" not in r and r["label"] == "pos"][: args.queries]
    (d / "one.txt").write_text(files[0] + "\n")
    (d / "list.txt").write_text("".join(f + "\n" for f in files))
    runs = {}
    db = audfprint_db(args.density)
    for name, lst in (("one", d / "one.txt"), ("many", d / "list.txt")):
        cmd = [str(AUDFPRINT_PY), str(AUDFPRINT), "match", "--dbase", str(db),
               "--density", str(args.density), "--ncores", "1", "--list", str(lst)]  # fmt: skip
        runs[f"audfprint_{name}"] = timed(cmd, d / f"audfprint_{name}.log")
        cmd = panako("query", *panako_conf(args.time_filter, 1), *lst.read_text().split())
        runs[f"panako_{name}"] = timed(cmd, d / f"panako_{name}.log")

    def per_query(tool: str) -> float:
        return (runs[f"{tool}_many"]["wall_s"] - runs[f"{tool}_one"]["wall_s"]) / (len(files) - 1)

    secs = {f"audfprint:{args.density:g}": per_query("audfprint"),
            f"panako:{args.time_filter}": per_query("panako")}  # fmt: skip
    with (  # one matcher per shard: one core per search
        cluster(INDEX, 1, 10000, 1, base_port=8200, stop_fraction=0.04) as (pc, _),
        cluster(B2_INDEX, 1, 10000, 1, base_port=8300, stop_fraction=0.04) as (bc, _),
    ):
        lm = run_landmark(d, 1, pc, bc)
    for system, g in lm.groupby("system"):
        secs[system] = float((g["decode_ms"] + g["fp_ms"] + g["search_ms"]).median()) / 1000
    result["query_s_per_clip_one_core"] = {k: round(v, 4) for k, v in secs.items()}
    result["query_runs"] = runs
    result["power_after"] = battery()
    shutil.rmtree(d)
    RES.mkdir(parents=True, exist_ok=True)
    (RES / "costs.json").write_text(json.dumps(result, indent=1) + "\n")
    print(json.dumps(result, indent=1))


# Scoring ------------------------------------------------------------------


def load_rows(split: str, cl: dict) -> pd.DataFrame:
    df = pd.concat(
        pd.read_csv(p, keep_default_na=False) for p in sorted(RES.glob(f"rows_{split}_*.csv.gz"))
    )
    if "error" not in df:
        df["error"] = ""
    df = df[df["error"] == ""].copy()  # failed searches: counted as skipped, not scored
    truth = df["fma_id"].map(lambda f: cl.get(f"fma:{f}", f"fma:{f}"))
    df["correct"] = (df["label"] == "pos") & (df["top1"].map(lambda t: cl.get(t, t)) == truth)
    return df


def accepted(df: pd.DataFrame, t_abs: float, t_ratio: float) -> np.ndarray:
    s = df["score"].to_numpy(dtype=float)
    return (s >= t_abs) & (s >= t_ratio * np.maximum(df["runner_up"].to_numpy(dtype=float), 1))


def calibrate(val: pd.DataFrame) -> dict:
    pos, neg = val[val.label == "pos"], val[val.label == "neg"]
    cands = np.unique(np.quantile(val["score"], np.linspace(0, 1, 301)).round())
    best = None
    for ta in cands[cands >= 1]:
        for tr in T_RATIO:
            far = float(accepted(neg, ta, tr).mean())
            if far > MAX_FAR:
                continue
            ok = accepted(pos, ta, tr) & pos["correct"].to_numpy()
            acc = float(pd.Series(ok).groupby(pos["cond"].to_numpy()).mean().mean())
            if best is None or acc > best["val_mean_accuracy"]:
                best = {"t_abs": float(ta), "t_ratio": tr, "val_mean_accuracy": acc, "val_far": far}
    if best is None:
        raise SystemExit("no threshold meets the false-accept limit")
    return best


def score(args: argparse.Namespace) -> None:
    cl = clusters()
    val, test = load_rows("val", cl), load_rows("test", cl)
    out = {}
    for system in args.systems:
        v, t = val[val.system == system], test[test.system == system]
        th = calibrate(v)
        conds = {}
        for cond in CONDITIONS:
            g = t[t.cond == cond]
            pos, neg = g[g.label == "pos"], g[g.label == "neg"]
            if not len(pos):
                continue
            k = int((accepted(pos, th["t_abs"], th["t_ratio"]) & pos["correct"].to_numpy()).sum())
            fa = int(accepted(neg, th["t_abs"], th["t_ratio"]).sum())
            conds[cond] = {"n_pos": len(pos), "accuracy": k / len(pos),
                           "accuracy_ci": wilson(k, len(pos)),
                           "raw_top1": float(pos["correct"].mean()), "n_neg": len(neg),
                           "far": fa / max(len(neg), 1)}  # fmt: skip
        out[system] = {"thresholds": th, "test": conds}
    (RES / "summary.json").write_text(json.dumps(out, indent=1) + "\n")
    names = list(out)
    print("| condition | " + " | ".join(names) + " |")
    print("|---|" + "---|" * len(names))
    for cond in CONDITIONS:
        cells = [
            f"{100 * out[s]['test'][cond]['accuracy']:.1f}%" if cond in out[s]["test"] else ""
            for s in names
        ]
        print(f"| {cond} | " + " | ".join(cells) + " |")


def main() -> None:
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("refs")
    a = sub.add_parser("index-audfprint")
    a.add_argument("--density", type=float, default=20)
    a.add_argument("--cores", type=int, default=os.cpu_count())
    p = sub.add_parser("index-panako")
    p.add_argument("--time-filter", type=int, default=25)
    p.add_argument("--list", type=Path, help="store only these files (resume)")
    p.add_argument("--cores", type=int, default=os.cpu_count())
    c = sub.add_parser("clips")
    c.add_argument("--split", choices=["val", "test"], required=True)
    c.add_argument(
        "--systems", nargs="+", required=True, help="landmark, audfprint:<density>, panako:<filter>"
    )
    c.add_argument("--conds", nargs="+", default=CONDITIONS)
    c.add_argument("--workers", type=int, default=8)
    c.add_argument("--cores", type=int, default=os.cpu_count())
    x = sub.add_parser("mixes")
    x.add_argument(
        "--systems", nargs="+", required=True, help="audfprint:<density>, panako:<filter>"
    )
    x.add_argument("--sets", nargs="+", help="UnmixDB sets (default: all)")
    x.add_argument("--batch", type=int, default=24, help="mixes cut into windows at once")
    x.add_argument("--workers", type=int, default=8)
    x.add_argument("--cores", type=int, default=os.cpu_count())
    k = sub.add_parser("cost")
    k.add_argument("--tracks", type=int, default=2000)
    k.add_argument("--queries", type=int, default=200)
    k.add_argument("--density", type=float, required=True, help="audfprint, as chosen on val")
    k.add_argument("--time-filter", type=int, required=True, help="Panako, as chosen on val")
    k.add_argument("--panako-cores", type=int, default=4)
    s = sub.add_parser("score")
    s.add_argument("--systems", nargs="+", required=True)
    args = ap.parse_args()
    {"refs": refs, "index-audfprint": index_audfprint, "index-panako": index_panako, "clips": clips,
     "mixes": mixes, "cost": cost, "score": score}[args.cmd](args)  # fmt: skip


if __name__ == "__main__":
    main()
