"""Choose how a tracklist entry's song is aligned (D55): three methods over
the same window rows, scored against UnmixDB's truth.

  python -m eval.alignment_compare

For every entry the eval's decode gives (the hybrid at its calibration, as
cmd/tracklist) on a known segment of the same file: the song's position at
the entry's start from (a) the median intercept of its triplet windows (D43),
(b) the consensus intercept (eval.dj_analysis.alignment), and (c) the window
intercept whose warped reference best fits the mix's spectrogram over the
entry (log-spectral error, either pitch, as D43 picks turntable or keylock).
Chosen on the calibration sets (set222, set275), reported on the test sets.
Writes eval/results/local/alignment_compare.{csv,json}.
"""

import json
import multiprocessing as mp
from pathlib import Path

import numpy as np
import pandas as pd

from eval.dj_analysis import (
    AGREE_S,
    FPS,
    MARGIN,
    WINDOW_S,
    alignment,
    power_spectrogram,
    ref_paths,
    warp,
)
from eval.tracklist_score import combine, decode_smooth, merge_repeats, pair_rows
from prototype.audio import decode

ROOT = Path(__file__).resolve().parent.parent
TRK = ROOT / "eval/results/tracklist"
OUT = ROOT / "eval/results/local"
CALIBRATION_SETS = ("set222", "set275")
_STATE: dict = {}


def fit_error(mixspec: np.ndarray, ref: np.ndarray, a: float, rate: float, start: float,
              end: float, pitch: bool) -> float:  # fmt: skip
    n = len(mixspec)
    w = warp(ref, a, rate, n, pitch)
    frames = slice(int(start * FPS), int(min(end, n / FPS) * FPS))
    m, s = mixspec[frames, MARGIN:-MARGIN], w[frames, MARGIN:-MARGIN]
    if s.sum() <= 0:
        return float("inf")
    scale = np.sum(m * s) / max(np.sum(s * s), 1e-20)
    return float(np.mean((np.log10(m + 1e-12) - np.log10(scale * s + 1e-12)) ** 2))


def one(k: int) -> list[dict]:
    m, rows, w, params, paths = (_STATE["gt"][k], _STATE["rows"], _STATE["w"],
                                 _STATE["params"], _STATE["paths"])  # fmt: skip
    mixrows = rows[rows["mix"] == k]
    entries = decode_smooth(mixrows, WINDOW_S, params["t_abs"], params["t_ratio"], params["k"])
    b2 = w[(w["mix"] == k) & (w["mode"] == "b2")]
    mixspec, out = None, []
    for e in merge_repeats(entries, 2.5):
        hits = [s for s in m["segments"] if s["known"] and s["dense"] == e.track
                and e.start < s["mix_end"] and e.end > s["mix_start"]]  # fmt: skip
        r = b2[
            (b2["top1_dense"] == e.track) & (b2["t0"] >= e.start - WINDOW_S) & (b2["t0"] <= e.end)
        ]
        if not hits or len(r) == 0 or e.track not in paths:
            continue
        s = hits[0]
        truth = s["src_start"] + s["factor"] * (e.start - s["mix_start"])
        med, rate = alignment(r, e.track, e.start, e.end, "median")
        cons, _ = alignment(r, e.track, e.start, e.end, "consensus")
        if mixspec is None:
            mixspec = power_spectrogram(decode(ROOT / m["file"]))
        ref = power_spectrogram(decode(paths[e.track]))
        cands = sorted({round(float(x), 3) for x in r["offset"] / FPS - rate * r["t0"]})
        errs = [(min(fit_error(mixspec, ref, x, rate, e.start, e.end, p) for p in (False, True)), x)
                for x in cands]  # fmt: skip
        fit = min(errs)[1]
        out.append({"mix": k, "set": m["set"], "timescale": m["timescale"], "effect": m["effect"],
                    "track": e.track, "start_s": e.start,
                    **{name: abs(x + rate * e.start - truth) for name, x
                       in (("median", med), ("consensus", cons), ("audio_fit", fit))}})  # fmt: skip
    return out


def summary(v: pd.Series) -> dict[str, float]:
    return {"n": int(len(v)), "median_s": float(v.median()),
            "within_0.05_s": float((v <= 0.05).mean()), "within_0.5_s": float((v <= 0.5).mean()),
            "over_0.5_s": int((v > 0.5).sum())}  # fmt: skip


def main() -> None:
    gt = json.loads((ROOT / "eval/mixes/unmixdb_s8.json").read_text())["mixes"]
    params = json.loads((TRK / "calibration_unmixdb_s8_b2dhp_hybrid_set222-set275_merged.json")
                        .read_text())["smooth"]["params"]  # fmt: skip
    w = pd.read_csv(TRK / "windows_unmixdb_s8_cli_h27520_d53.csv.gz")
    pairs = pair_rows(pd.read_csv(TRK / "windows_unmixdb_s8_cli_h27520_d53_pairs.csv.gz"))
    _STATE.update(gt=gt, w=w, params=params, rows=combine(w, pairs, params["alpha"]),
                  paths=ref_paths("data/segments/medium_unmixdb_b2"))  # fmt: skip
    with mp.get_context("fork").Pool(10) as pool:
        df = pd.DataFrame([x for part in pool.map(one, range(len(gt))) for x in part])
    OUT.mkdir(parents=True, exist_ok=True)
    df.to_csv(OUT / "alignment_compare.csv", index=False)
    cal = df["set"].isin(CALIBRATION_SETS)
    result = {"rows": "windows_unmixdb_s8_cli_h27520_d53", "agree_s": AGREE_S,
              **{split: {meth: summary(d[meth]) for meth in ("median", "consensus", "audio_fit")}
                 for split, d in (("calibration", df[cal]), ("test", df[~cal]))}}  # fmt: skip
    (OUT / "alignment_compare.json").write_text(json.dumps(result, indent=1) + "\n")
    print(json.dumps(result, indent=1))


if __name__ == "__main__":
    main()
