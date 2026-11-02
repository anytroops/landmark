"""DJ mixes as a phone in the room would record them: tracklisting under a
simulated room and crowd noise (degrade.room, D52 noise).

  python -m eval.room_mixes --condition room_rt800_drr-5_demand_pcafeter_snr5
  python -m eval.room_mixes --score-runs eval/results/local/runs_d55.jsonl --tag _clean

Renders every test-set mix of unmixdb_s8 through the condition (the whole
mix, so each song's reverberation reaches into the next; seeded per mix)
into data/queries/room/<condition>/, runs bin/tracklist -local on each (the
shipped settings), and scores the entries as the DJ evals do
(eval.tracklist_score.score_mix, the merge gap applied by the command).
Writes eval/results/local/room<tag>.json and the runs as room<tag>.jsonl.
"""

import argparse
import concurrent.futures
import json
import re
import subprocess
from pathlib import Path

import numpy as np
import soundfile as sf

from bench.scripts.benchcluster import BIN
from bench.scripts.live import B2_INDEX, PAIR_INDEX, git_head
from eval import demand
from eval.degrade import add_noise, room
from eval.tracklist import cluster_table
from eval.tracklist_score import Entry, score_mix
from prototype.audio import SAMPLE_RATE, decode

ROOT = Path(__file__).resolve().parent.parent
RES = ROOT / "eval" / "results" / "local"
TEST_SETS = ("set044", "set123", "set230", "set281")
_COND = re.compile(r"(?:room_rt(\d+)_drr(-?\d+))?_?(?:demand_([a-z]+)_snr(-?\d+))?")


def render(pcm: np.ndarray, cond: str, rng: np.random.Generator) -> np.ndarray:
    """The mix through the room (if named), then the noise (if named)."""
    m = _COND.fullmatch(cond)
    if m is None or not cond or cond.startswith("_") or cond.endswith("_"):
        raise SystemExit(f"condition {cond!r}: want room_rt<ms>_drr<db>, demand_<env>_snr<db>, "
                         "or both joined by _")  # fmt: skip
    y = pcm
    if m.group(1):
        y = room(y, int(m.group(1)) / 1000, float(m.group(2)), rng)
    if m.group(3):
        y = add_noise(y, float(m.group(4)), rng, demand.noise(m.group(3).upper(), "test"))
    return y


def score(runs: list[dict], gt: dict) -> dict:
    by_file = {m["file"]: m for m in gt["mixes"]}
    cluster = cluster_table(str(ROOT / "eval/manifests/splits_medium_seed0.json"),
                            str(B2_INDEX / "catalog.json"))  # fmt: skip
    tot = {"entries": 0, "correct": 0, "known": 0, "found": 0}
    for r in runs:
        entries = [Entry(e["track"], e["start_s"], e["end_s"], 1 + e["tempo_pct"] / 100,
                         e["evidence"]) for e in r["entries"]]  # fmt: skip
        s = score_mix(entries, by_file[r["file"]]["segments"], cluster)
        for k in tot:
            tot[k] += s[k]
    p = tot["correct"] / max(tot["entries"], 1)
    rc = tot["found"] / max(tot["known"], 1)
    return {**tot, "precision": p, "recall": rc, "f1": 2 * p * rc / max(p + rc, 1e-12)}


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--condition", help="room_rt<ms>_drr<db>, demand_<env>_snr<db>, or both")
    ap.add_argument("--score-runs", type=Path, help="only score existing runs (JSON lines)")
    ap.add_argument("--tag", default="")
    ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--parallel", type=int, default=2)
    args = ap.parse_args()
    gt = json.loads((ROOT / "eval/mixes/unmixdb_s8.json").read_text())
    if args.score_runs:
        runs = [json.loads(line) for line in args.score_runs.read_text().splitlines()]
        result = {"runs": str(args.score_runs), **score(runs, gt)}
    else:
        if not args.condition:
            ap.error("--condition or --score-runs")
        out_dir = ROOT / "data/queries/room" / args.condition
        out_dir.mkdir(parents=True, exist_ok=True)
        mixes = [(i, m) for i, m in enumerate(gt["mixes"]) if m["set"] in TEST_SETS]

        def one(job: tuple[int, dict]) -> dict:
            i, m = job
            path = out_dir / Path(m["file"]).name
            if not path.exists():
                rng = np.random.default_rng([args.seed, i])
                sf.write(path, render(decode(ROOT / m["file"]), args.condition, rng), SAMPLE_RATE)
            out = subprocess.run(
                [str(BIN / "tracklist"), "-local", "-index", str(B2_INDEX),
                 "-pairs-index", str(PAIR_INDEX), "-timeout", "10s", "-json", str(path)],
                capture_output=True, text=True, check=True,
            ).stdout  # fmt: skip
            return {"file": m["file"], "entries": json.loads(out)}

        with concurrent.futures.ThreadPoolExecutor(args.parallel) as ex:
            runs = list(ex.map(one, mixes))
        tag = args.tag or "_" + args.condition
        RES.mkdir(parents=True, exist_ok=True)
        (RES / f"room{tag}.jsonl").write_text("".join(json.dumps(r) + "\n" for r in runs))
        result = {"condition": args.condition, "seed": args.seed, **score(runs, gt), **git_head()}
    tag = args.tag or "_" + (args.condition or "")
    (RES / f"room{tag}.json").write_text(json.dumps(result, indent=1) + "\n")
    print(json.dumps(result))


if __name__ == "__main__":
    main()
