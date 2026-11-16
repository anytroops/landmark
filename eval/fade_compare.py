"""Fades read from different gain curves, on the same songs
(docs/design/mixer-moves.md, evaluation 2).

  python -m eval.fade_compare --out eval/results/mixer/fades_lr1e-3.json \\
      d43=eval/results/dj_analysis/test_consensus_cli_h27520_d53 b1=... model=...

Each run is `eval.dj_analysis --gains ...` on the same mixes, rows, and
alignments. A method skips a song whose gain is zero wherever it plays
alone, and which songs those are differs by method, so each fade edge is
scored over the songs every run reports for it (the label filters as
eval.dj_analysis.summarize: the true fade longer than 0.5 s), as well as
over each run's own songs.
"""

import argparse
import json
from pathlib import Path

import numpy as np

EDGES = [(kind, which, name) for kind in ("fade_in", "fade_out")
         for which, name in ((0, "start"), (1, "end"))]  # fmt: skip


def errors(songs: list[dict], keys: set | None) -> dict:
    out = {}
    for kind, which, name in EDGES:
        e = [abs(r["ramp_" + kind][which] - r["true_" + kind][which]) for r in songs
             if r.get("known") and "ramp_" + kind in r and "true_" + kind in r
             and r["true_" + kind][1] - r["true_" + kind][0] > 0.5 and kind in r
             and (keys is None or (r["mix"], r["track"], kind) in keys)]  # fmt: skip
        out[f"{kind}_{name}"] = {"n": len(e), "median": round(float(np.median(e)), 2),
                                 "p90": round(float(np.percentile(e, 90)), 2)}  # fmt: skip
    rm = [r["gain_rmse_in_fades"] for r in songs if "gain_rmse_in_fades" in r
          and (keys is None or (r["mix"], r["track"], "fade_in") in keys
               or (r["mix"], r["track"], "fade_out") in keys)]  # fmt: skip
    out["gain_rmse_in_fades"] = {"n": len(rm), "median": round(float(np.median(rm)), 3)}
    return out


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("runs", nargs="+", help="name=directory holding songs.json")
    ap.add_argument("--out", type=Path, required=True)
    args = ap.parse_args()
    runs = {}
    for spec in args.runs:
        name, path = spec.split("=", 1)
        runs[name] = json.loads((Path(path) / "songs.json").read_text())
    common = None
    for songs in runs.values():
        have = {(r["mix"], r["track"], kind) for r in songs for kind in ("fade_in", "fade_out")
                if "ramp_" + kind in r and kind in r}  # fmt: skip
        common = have if common is None else common & have
    result = {"runs": {n: str(p) for n, p in (s.split("=", 1) for s in args.runs)},
              "own_songs": {n: errors(s, None) for n, s in runs.items()},
              "common_songs": {n: errors(s, common) for n, s in runs.items()}}  # fmt: skip
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(result, indent=1) + "\n")
    print(json.dumps(result["common_songs"], indent=1))


if __name__ == "__main__":
    main()
