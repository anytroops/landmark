"""BPM for DJ mix analysis (D43 A2), Python prototype.

  python -m eval.dj_bpm --songs eval/results/dj_analysis/test/songs.json \\
      --out eval/results/dj_analysis/test

Each reference excerpt's tempo is estimated once, as ingest would, with
librosa's beat tracker, and checked against UnmixDB's own BPM for it (the
excerpt's .txt file, from IRCAM's beat tracker): within 4% (the MIR
"accuracy 1" tolerance), and within 4% of the label, twice it, or half it
("accuracy 2", which forgives the octave errors beat trackers make). A
song's tempo in the set is its reference BPM times its playback rate from
the triplet fit (eval.dj_analysis); the truth is the label's BPM times the
mix's speed factor for that song.
"""

import argparse
import csv
import json
import multiprocessing
from pathlib import Path

import librosa
import numpy as np

from prototype.audio import decode

ROOT = Path(__file__).resolve().parent.parent
TOLERANCE = 0.04
RATE = 22_050


def labeled_bpm() -> dict[str, float]:
    """UnmixDB's BPM per reference, keyed set/file as the catalog names them."""
    out = {}
    for txt in (ROOT / "data/unmixdb/extracted").glob(
        "mixotic-*-excerpts/refsongs/*.excerpt40.txt"
    ):
        s = txt.parts[-3].split("-")[1]
        with txt.open() as fh:
            for row in csv.DictReader(fh, delimiter="\t"):
                out[f"{s}/{row['filename'].strip(chr(34))}"] = float(row["bpm"])
    return out


def estimate(path: str) -> float:
    y = decode(path, RATE)
    tempo, _ = librosa.beat.beat_track(y=y, sr=RATE)
    return float(np.atleast_1d(tempo)[0])


def accuracy(est: float, true: float) -> tuple[bool, bool]:
    close = abs(est - true) <= TOLERANCE * true
    octave = any(abs(est - k * true) <= TOLERANCE * k * true for k in (1, 2, 0.5, 3, 1 / 3))
    return close, octave


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--songs", type=Path, required=True, help="eval.dj_analysis songs.json")
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--workers", type=int, default=6)
    args = ap.parse_args()
    extras = json.loads((ROOT / "data/segments/medium_unmixdb_b2/extras.json").read_text())
    cat = json.loads((ROOT / "data/segments/medium_unmixdb_b2/catalog.json").read_text())
    by_fma = {e["fma_id"]: e for e in extras}
    refs = {t["dense"]: by_fma[t["fma_id"]] for t in cat if t["fma_id"] in by_fma}
    labels = labeled_bpm()
    songs = json.loads(args.songs.read_text())
    dense = sorted({s["track"] for s in songs if s["track"] in refs})
    # spawn, not fork: forked workers deadlocked once librosa's threads had started
    with multiprocessing.get_context("spawn").Pool(args.workers) as pool:
        est = dict(zip(dense, pool.map(estimate, [refs[d]["path"] for d in dense]), strict=True))

    ref_rows = []
    for d in dense:
        true = labels.get(refs[d]["name"])
        if true is None:
            continue
        a1, a2 = accuracy(est[d], true)
        ref_rows.append(
            {
                "track": d,
                "name": refs[d]["name"],
                "bpm": est[d],
                "true_bpm": true,
                "acc1": a1,
                "acc2": a2,
            }
        )

    set_rows = []
    for s in songs:
        d = s["track"]
        if d not in est or "true_rate" not in s or refs[d]["name"] not in labels:
            continue
        got, true = est[d] * s["rate"], labels[refs[d]["name"]] * s["true_rate"]
        a1, a2 = accuracy(got, true)
        set_rows.append({"name": s["name"], "track": d, "set_bpm": got, "true_set_bpm": true,
                         "acc1": a1, "acc2": a2,
                         "rate_err_pct": 100 * abs(s["rate"] - s["true_rate"])})  # fmt: skip

    def share(rows: list[dict], key: str) -> float:
        return round(float(np.mean([r[key] for r in rows])), 3) if rows else float("nan")

    summary = {
        "references": len(ref_rows), "reference_acc1": share(ref_rows, "acc1"),
        "reference_acc2": share(ref_rows, "acc2"),
        "songs": len(set_rows), "set_bpm_acc1": share(set_rows, "acc1"),
        "set_bpm_acc2": share(set_rows, "acc2"),
        "rate_err_pct_median": round(float(np.median([r["rate_err_pct"] for r in set_rows])), 3),
        "tolerance": TOLERANCE,
    }  # fmt: skip
    args.out.mkdir(parents=True, exist_ok=True)
    (args.out / "bpm_refs.json").write_text(json.dumps(ref_rows, indent=1) + "\n")
    (args.out / "bpm_summary.json").write_text(json.dumps(summary, indent=1) + "\n")
    print(json.dumps(summary, indent=1))


if __name__ == "__main__":
    main()
