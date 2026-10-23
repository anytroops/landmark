"""Write the window rows cmd/tracklist searches, in eval.tracklist's format,
so eval.tracklist_score can calibrate and score the decoder on them: for
example at another window hop (throughput option T1 (a)).

  python -m bench.scripts.tracklist_rows --hop 2.4961452 --tag _cli_h27520 \\
      --sets set222 set275 set044 set123 set230 set281

Starts the triplet and pair clusters (stop list 4%, as the hybrid was
calibrated), runs bin/tracklist -alpha 1 -rows on every mix of the sets,
and writes eval/results/tracklist/windows_unmixdb_s8<tag>.csv.gz (the
triplet searches' rows) and windows_unmixdb_s8<tag>_pairs.csv.gz (the pair
searches' rows, unweighted), each with a meta.json. Mix numbers are
positions in eval/mixes/unmixdb_s8.json, as eval.tracklist writes them.
"""

import argparse
import concurrent.futures
import gzip
import json
import subprocess
import tempfile
from pathlib import Path

from bench.scripts.benchcluster import BIN, ROOT, cluster
from bench.scripts.live import B2_INDEX, PAIR_INDEX, git_head

RES = ROOT / "eval/results/tracklist"
PAIR_MODES = ("none", "keylock")


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--mixes", default="unmixdb_s8")
    ap.add_argument("--sets", nargs="+", required=True)
    ap.add_argument("--hop", type=float, default=2.5)
    ap.add_argument("--snap", type=int, default=0, help="cmd/tracklist -snap (throughput T1)")
    ap.add_argument("--tag", required=True)
    ap.add_argument("--parallel", type=int, default=3, help="tracklist processes at once")
    ap.add_argument("--timeout", default="10s", help="per-search deadline")
    ap.add_argument("--limit", type=int, default=0, help="first N mixes only (smoke test)")
    args = ap.parse_args()
    gt = json.loads((ROOT / "eval/mixes" / f"{args.mixes}.json").read_text())
    todo = [(i, m["file"]) for i, m in enumerate(gt["mixes"]) if m["set"] in args.sets]
    todo = todo[: args.limit or None]
    tmp = Path(tempfile.mkdtemp(prefix="tracklist-rows-"))
    head = git_head()

    with (
        cluster(B2_INDEX, 1, 10000, 8, base_port=7100, stop_fraction=0.04) as (b2, _),
        cluster(PAIR_INDEX, 1, 10000, 8, base_port=7200, stop_fraction=0.04) as (pairs, _),
    ):

        def one(job: tuple[int, str]) -> tuple[int, str]:
            i, f = job
            out = tmp / f"{i}.csv"
            subprocess.run(
                [str(BIN / "tracklist"), "-coordinator", b2, "-index", str(B2_INDEX),
                 "-pairs-coordinator", pairs, "-pairs-index", str(PAIR_INDEX),
                 "-timeout", args.timeout, "-hop", repr(args.hop), "-snap", str(args.snap),
                 "-alpha", "1",
                 "-rows", str(out), "-json", str(ROOT / f)],
                capture_output=True, text=True, check=True,
            )  # fmt: skip
            return i, out.read_text()

        with concurrent.futures.ThreadPoolExecutor(args.parallel) as ex:
            done = dict(ex.map(one, todo))

    header = "mix,t0,mode,factor,top1_dense,score,runner_up,offset\n"
    files = {
        "triplets": RES / f"windows_{args.mixes}{args.tag}.csv.gz",
        "pairs": RES / f"windows_{args.mixes}{args.tag}_pairs.csv.gz",
    }
    counts = {"triplets": 0, "pairs": 0}
    with (
        gzip.open(files["triplets"], "wt") as trip,
        gzip.open(files["pairs"], "wt") as pair,
    ):
        trip.write(header)
        pair.write(header)
        for i, _ in todo:
            for line in done[i].splitlines()[1:]:
                kind = "pairs" if line.split(",")[1] in PAIR_MODES else "triplets"
                (pair if kind == "pairs" else trip).write(f"{i},{line}\n")
                counts[kind] += 1
    for kind, path in files.items():
        meta = {"mixes": args.mixes, "source": "cmd/tracklist -rows -alpha 1", "window_s": 10.0,
                "hop_s": args.hop, "snap_samples": args.snap, "sets": args.sets,
                "stop_fraction": 0.04, "rows": counts[kind], "mixes_searched": len(todo),
                **head}  # fmt: skip
        path.with_name(path.name.replace(".csv.gz", ".meta.json")).write_text(
            json.dumps(meta, indent=1) + "\n"
        )
    print(json.dumps({"mixes": len(todo), **counts, "hop_s": args.hop}))


if __name__ == "__main__":
    main()
