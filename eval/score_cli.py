"""Score cmd/tracklist's output against ground truth, and compare with the eval.

  python -m eval.score_cli --mixes unmixdb_s8 --runs out.jsonl --sets set044 \\
      --tag _b2dh --pair-tag _gridA2 --catalog data/segments/medium_unmixdb_b2/catalog.json \\
      --params eval/results/tracklist/calibration_unmixdb_s8_b2dh_hybrid_set222-set275.json

--runs is JSON lines, one per mix: {"file": <mix path as in the mixes file>,
"entries": <cmd/tracklist -json output>}. Scores those entries with
eval.tracklist_score.score_mix, then decodes the same mixes from the eval's
window rows (the hybrid, with the calibrated smoothing settings and the
same merge gap) and prints both, so a difference between the shipped command
and the evaluated system shows up.
"""

import argparse
import json
from pathlib import Path

import pandas as pd

from eval.tracklist import cluster_table
from eval.tracklist_score import (
    RES,
    ROOT,
    Entry,
    combine,
    decode_smooth,
    merge_repeats,
    pair_rows,
    score_mix,
)


def totals(results: list[dict]) -> str:
    t = {k: sum(r[k] for r in results) for k in ("entries", "correct", "known", "found")}
    found = 100 * t["found"] / max(t["known"], 1)
    precision = 100 * t["correct"] / max(t["entries"], 1)
    return (f"found {t['found']}/{t['known']} ({found:.1f}%), "
            f"precision {precision:.1f}% over {t['entries']} entries")  # fmt: skip


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--mixes", required=True)
    ap.add_argument("--runs", type=Path, required=True)
    ap.add_argument("--sets", nargs="+", required=True)
    ap.add_argument("--tag", required=True)
    ap.add_argument("--pair-tag", required=True)
    ap.add_argument("--catalog", required=True)
    ap.add_argument("--params", type=Path, required=True)
    ap.add_argument("--merge-gap", type=float, default=2.5)
    args = ap.parse_args()
    gt = json.loads((ROOT / "eval" / "mixes" / f"{args.mixes}.json").read_text())
    cluster = cluster_table("", args.catalog)
    runs = {}
    for line in args.runs.read_text().splitlines():
        r = json.loads(line)
        runs[r["file"]] = [Entry(e["track"], e["start_s"], e["end_s"], 1 + e["tempo_pct"] / 100,
                                 e["evidence"]) for e in r["entries"]]  # fmt: skip
    idx = [i for i, m in enumerate(gt["mixes"]) if m["set"] in args.sets and m["file"] in runs]
    print(f"{len(idx)} mixes with command output")

    segments = {i: gt["mixes"][i]["segments"] for i in idx}
    cli = [score_mix(runs[gt["mixes"][i]["file"]], segments[i], cluster) for i in idx]
    p = json.loads(args.params.read_text())["smooth"]["params"]
    w = pd.read_csv(RES / f"windows_{args.mixes}{args.tag}.csv.gz")
    pairs = pair_rows(pd.read_csv(RES / f"windows_{args.mixes}{args.pair_tag}.csv.gz"))
    rows = combine(w[w["mix"].isin(idx)], pairs[pairs["mix"].isin(idx)], p["alpha"])
    meta = json.loads((RES / f"windows_{args.mixes}{args.tag}.meta.json").read_text())
    ev, differ = [], 0
    for i in idx:
        wi = rows[rows["mix"] == i]
        e = merge_repeats(decode_smooth(wi, meta["window_s"], p["t_abs"], p["t_ratio"], p["k"]),
                          args.merge_gap)  # fmt: skip
        ev.append(score_mix(e, segments[i], cluster))
        got = runs[gt["mixes"][i]["file"]]
        differ += len(got) != len(e) or any(
            a.track != b.track or abs(a.start - b.start) > 0.01 or abs(a.end - b.end) > 0.01
            for a, b in zip(got, e, strict=True)
        )
    print("cmd/tracklist:", totals(cli))
    print("eval decode:  ", totals(ev))
    print(f"mixes whose entries differ (track, or times by over 10 ms): {differ} of {len(idx)}")


if __name__ == "__main__":
    main()
