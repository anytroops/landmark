"""BPM for DJ mix analysis (D43 A2), Python prototype.

  python -m eval.dj_bpm --songs eval/results/dj_analysis/test/songs.json \\
      --out eval/results/dj_analysis/test
  python -m eval.dj_bpm --index data/segments/medium_unmixdb_b2   # write bpm.json

With --index, it writes <index>/bpm.json (dense id -> reference BPM) for the
index's non-FMA references, which `cmd/tracklist -analyze` multiplies by
each song's fitted playback rate.

Each reference excerpt's tempo is estimated once, as ingest would, with
librosa's global tempo estimate (onset strength, then an autocorrelation
tempogram weighted by a log-normal prior; its beat tracker segfaults in
this environment, and the tempo alone is what the set's BPM needs), and
checked against UnmixDB's own BPM for it (the
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
# Prior for club music, chosen on the calibration sets' 25 references
# (librosa's default, 120 BPM with a one-octave width: 19 within 4%; 125
# BPM with half an octave: 24): estimators confuse a tempo with its double,
# half, or 3:2 and 4:3 relatives, and a DJ set's songs sit in a narrow band.
PRIOR_BPM, PRIOR_OCTAVES = 125.0, 0.5


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
    onset = librosa.onset.onset_strength(y=y, sr=RATE)
    tempo = librosa.feature.tempo(
        onset_envelope=onset, sr=RATE, start_bpm=PRIOR_BPM, std_bpm=PRIOR_OCTAVES
    )
    return float(np.atleast_1d(tempo)[0])


def accuracy(est: float, true: float) -> tuple[bool, bool]:
    close = abs(est - true) <= TOLERANCE * true
    octave = any(abs(est - k * true) <= TOLERANCE * k * true for k in (1, 2, 0.5, 3, 1 / 3))
    return close, octave


def write_index(index: Path, workers: int) -> None:
    cat = json.loads((index / "catalog.json").read_text())
    by_fma = {e["fma_id"]: e for e in json.loads((index / "extras.json").read_text())}
    dense = [t["dense"] for t in cat if t["fma_id"] in by_fma]
    paths = [by_fma[t["fma_id"]]["path"] for t in cat if t["fma_id"] in by_fma]
    with multiprocessing.get_context("spawn").Pool(workers) as pool:
        bpm = pool.map(estimate, paths)
    out = {str(d): round(b, 3) for d, b in zip(dense, bpm, strict=True)}
    (index / "bpm.json").write_text(json.dumps(out, indent=1) + "\n")
    print(f"{len(out)} references -> {index / 'bpm.json'}")


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--songs", type=Path, help="eval.dj_analysis songs.json")
    ap.add_argument("--out", type=Path)
    ap.add_argument("--index", type=Path, help="write this index's bpm.json instead")
    ap.add_argument("--workers", type=int, default=6)
    args = ap.parse_args()
    if args.index:
        write_index(args.index, args.workers)
        return
    if not args.songs or not args.out:
        ap.error("--songs and --out, or --index")
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
