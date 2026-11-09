"""Measure eval.verify_set on UnmixDB, where the truth is known.

  python -m eval.verify_calibrate

Positives: entries the decoder gives on the right file. Negatives: its wrong
entries, and up to 5 per mix of the triplet search's top answers that name a
song not playing then (what the fingerprints said, wrongly). The pass mark is
the smallest z letting through at most 1% of the calibration sets' negatives
(set222, set275); reported on the test sets. Writes
eval/results/local/verify_calibration.{csv,json}.
"""

import json
import multiprocessing as mp
from pathlib import Path

import numpy as np
import pandas as pd

from eval.dj_analysis import WINDOW_S
from eval.tracklist_score import combine, decode_smooth, merge_repeats, pair_rows
from eval.verify_set import log_spec, verify_entries
from prototype.audio import DecodeError, decode
from prototype.fma import audio_path

ROOT = Path(__file__).resolve().parent.parent
TRK = ROOT / "eval/results/tracklist"
OUT = ROOT / "eval/results/local"
INDEX = ROOT / "data/segments/medium_unmixdb_b2"
CAL = ("set222", "set275")
MAX_FAR = 0.01
_S: dict = {}


def load_ref(t: int) -> np.ndarray:
    cache = _S.setdefault("cache", {})
    if t not in cache:
        path = _S["paths"].get(t) or audio_path(_S["fma"][t], "medium")
        cache[t] = log_spec(decode(Path(path)))
    return cache[t]


def one(k: int) -> list[dict]:
    m = _S["gt"][k]
    p = _S["params"]
    rows = _S["rows"][_S["rows"]["mix"] == k]
    b2 = _S["w"][(_S["w"]["mix"] == k) & (_S["w"]["mode"] == "b2")]
    entries = merge_repeats(decode_smooth(rows, WINDOW_S, p["t_abs"], p["t_ratio"], p["k"]), 2.5)
    segs = [s for s in m["segments"] if s["known"]]

    def plays(track: int, a: float, b: float) -> bool:
        return any(s["dense"] == track and a < s["mix_end"] and b > s["mix_start"] for s in segs)

    items = [
        (
            {"track": e.track, "start_s": e.start, "end_s": e.end},
            b2,
            plays(e.track, e.start, e.end),
            "entry",
        )
        for e in entries
    ]
    wrong = b2[[not plays(int(r.top1_dense), r.t0, r.t0 + WINDOW_S) for r in b2.itertuples()]]
    wrong = wrong[wrong["top1_dense"] >= 0]
    rng = np.random.default_rng(k)
    for i in rng.permutation(len(wrong))[:5]:
        r = wrong.iloc[[int(i)]]
        t0 = float(r["t0"].iloc[0])
        item = {"track": int(r["top1_dense"].iloc[0]), "start_s": t0, "end_s": t0 + WINDOW_S}
        items.append((item, r, False, "row"))
    try:
        mix = log_spec(decode(ROOT / m["file"]))
    except DecodeError:
        return []
    out = []
    for item, src, label, kind in items:
        try:
            v = verify_entries(mix, [item], src, load_ref, _S["pool"], seed=k)[0]
        except DecodeError:
            continue
        out.append(
            {
                "mix": k,
                "set": m["set"],
                "timescale": m["timescale"],
                "effect": m["effect"],
                "kind": kind,
                "right": label,
                "track": item["track"],
                "z": v["z"],
                "corr": v["corr"],
            }
        )
    return out


def main() -> None:
    gt = json.loads((ROOT / "eval/mixes/unmixdb_s8.json").read_text())["mixes"]
    params = json.loads(
        (TRK / "calibration_unmixdb_s8_b2dhp_hybrid_set222-set275_merged.json").read_text()
    )["smooth"]["params"]
    w = pd.read_csv(TRK / "windows_unmixdb_s8_cli_h27520_d53.csv.gz")
    pairs = pair_rows(pd.read_csv(TRK / "windows_unmixdb_s8_cli_h27520_d53_pairs.csv.gz"))
    extras = json.loads((INDEX / "extras.json").read_text())
    catalog = json.loads((INDEX / "catalog.json").read_text())
    paths = {int(x["dense"]): x["path"] for x in extras}
    pool = [int(x) for x in np.random.default_rng(1).choice(sorted(paths), size=60, replace=False)]
    _S.update(
        gt=gt,
        params=params,
        w=w,
        rows=combine(w, pairs, params["alpha"]),
        paths=paths,
        fma={int(t["dense"]): int(t["fma_id"]) for t in catalog},
        pool=pool,
    )
    with mp.get_context("fork").Pool(8) as p:
        df = pd.DataFrame([x for part in p.map(one, range(len(gt))) for x in part])
    OUT.mkdir(parents=True, exist_ok=True)
    df.to_csv(OUT / "verify_calibration.csv", index=False)
    df["z"] = df["z"].fillna(-99.0)
    cal = df["set"].isin(CAL)
    neg = np.sort(df[cal & ~df["right"]]["z"].to_numpy())
    mark = float(neg[min(int(np.ceil((1 - MAX_FAR) * len(neg))), len(neg) - 1)])

    def rates(d: pd.DataFrame) -> dict:
        pos, n = d[d["right"]], d[~d["right"]]
        return {
            "positives": int(len(pos)),
            "negatives": int(len(n)),
            "passed_right": float((pos["z"] >= mark).mean()),
            "passed_wrong": float((n["z"] >= mark).mean()),
            "wrong_entries": int((~d["right"] & (d["kind"] == "entry")).sum()),
            "wrong_entries_passed": int(
                ((~d["right"]) & (d["kind"] == "entry") & (d["z"] >= mark)).sum()
            ),
        }

    result = {
        "z_mark": mark,
        "max_far": MAX_FAR,
        "calibration": rates(df[cal]),
        "test": rates(df[~cal]),
        "test_by_timescale": {t: rates(g) for t, g in df[~cal].groupby("timescale")},
        "test_by_effect": {t: rates(g) for t, g in df[~cal].groupby("effect")},
    }
    (OUT / "verify_calibration.json").write_text(json.dumps(result, indent=1) + "\n")
    print(json.dumps(result, indent=1))


if __name__ == "__main__":
    main()
