"""How far tracklist's source positions (the set reconstructor's cue points,
D54) are from the truth.

  python -m eval.cue_accuracy --runs eval/results/local/runs_d54.jsonl --tag _d54

--runs is JSON lines, one per mix: {"file": <mix path as in the mixes file>,
"entries": <tracklist -json output>}. For each entry on the right song (the
same file as a known segment it overlaps, not only a duplicate of it), the
song's true position at the entry's start and end is
src_start + factor * (t - mix_start), from the mixes file; the errors are
|reported - true|, in seconds of the song. Writes
eval/results/local/cue_accuracy<tag>.json.
"""

import argparse
import json
from pathlib import Path

import numpy as np

ROOT = Path(__file__).resolve().parent.parent
RES = ROOT / "eval" / "results" / "local"


def errors(entries: list[dict], segments: list[dict]) -> list[tuple[float, float]]:
    """(start error, end error) for every entry with source positions on a
    known segment of the same file."""
    out = []
    for e in entries:
        if e.get("source_start_s") is None:
            continue
        hits = [s for s in segments if s["known"] and s["dense"] == e["track"]
                and e["start_s"] < s["mix_end"] and e["end_s"] > s["mix_start"]]  # fmt: skip
        if not hits:
            continue
        s = hits[0]

        def truth(t: float, s: dict = s) -> float:
            return s["src_start"] + s["factor"] * (t - s["mix_start"])

        out.append((abs(e["source_start_s"] - truth(e["start_s"])),
                    abs(e["source_end_s"] - truth(e["end_s"]))))  # fmt: skip
    return out


def summary(errs: list[float]) -> dict[str, float]:
    x = np.array(errs)
    if x.size == 0:
        return {"n": 0}
    out = {"n": int(x.size), "median_s": float(np.median(x)),
           "p90_s": float(np.quantile(x, 0.9)), "max_s": float(x.max())}  # fmt: skip
    for limit in (0.05, 0.1, 0.5):
        out[f"within_{limit}_s"] = float((x <= limit).mean())
    return out


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--runs", type=Path, required=True)
    ap.add_argument("--mixes", default="unmixdb_s8")
    ap.add_argument("--tag", default="")
    args = ap.parse_args()
    gt = json.loads((ROOT / "eval" / "mixes" / f"{args.mixes}.json").read_text())
    by_file = {m["file"]: m for m in gt["mixes"]}
    groups: dict[str, list[tuple[float, float]]] = {}
    for line in args.runs.read_text().splitlines():
        r = json.loads(line)
        m = by_file[r["file"]]
        groups.setdefault(m["timescale"], []).extend(errors(r["entries"], m["segments"]))
    every = [e for g in groups.values() for e in g]

    def both(g: list[tuple[float, float]]) -> dict:
        return {"in": summary([a for a, _ in g]), "out": summary([b for _, b in g])}

    result = {"runs": str(args.runs), "mixes": args.mixes, **both(every),
              "by_timescale": {k: both(g) for k, g in sorted(groups.items())}}  # fmt: skip
    RES.mkdir(parents=True, exist_ok=True)
    (RES / f"cue_accuracy{args.tag}.json").write_text(json.dumps(result, indent=1) + "\n")
    print(json.dumps(result, indent=1))


if __name__ == "__main__":
    main()
