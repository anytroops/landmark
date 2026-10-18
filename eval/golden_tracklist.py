"""Export golden tracklist decodes for the Go port (internal/tracklist).

  python -m eval.golden_tracklist

Takes a few mixes' window rows from committed eval.tracklist runs (B2: one
row per window; speed grid: one per window and hypothesis) and decodes them
with eval.tracklist_score's smoothing and line-fit + Viterbi decoders at
fixed calibrated settings (parity needs only that both sides use the same
ones). Writes the rows and the entries to testdata/golden_tracklist/*.json;
Go must produce the same entries.
"""

import json
from pathlib import Path

import pandas as pd

from eval.tracklist_score import decode_align, decode_smooth

ROOT = Path(__file__).resolve().parent.parent
RES = ROOT / "eval" / "results" / "tracklist"
OUT = ROOT / "testdata" / "golden_tracklist"
# (windows file stem, calibration file, mixes to export)
CASES = [
    ("windows_test_mixed8_b2", "calibration_val_mixed8_gridA.json", [0, 7]),
    ("windows_test_hard8_gridA", "calibration_val_hard8_gridA.json", [3]),
]
COLS = ["t0", "mode", "factor", "top1_dense", "score", "runner_up", "offset"]


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    for stem, calib, mixes in CASES:
        w = pd.read_csv(RES / f"{stem}.csv.gz")
        window = json.loads((RES / f"{stem}.meta.json").read_text())["window_s"]
        params = json.loads((RES / calib).read_text())
        for k in mixes:
            wk = w[w["mix"] == k].sort_values(["t0", "mode", "factor"]).reset_index(drop=True)
            sp, ap = params["smooth"]["params"], params["align"]["params"]
            smooth = decode_smooth(wk, window, sp["t_abs"], sp["t_ratio"], sp.get("k", 1))
            align = decode_align(wk, window, ap["min_evidence"], ap["switch"])
            doc = {
                "source": f"{stem} mix {k}",
                "window_s": window,
                "smooth_params": sp,
                "align_params": ap,
                "rows": {c: wk[c].tolist() for c in COLS},
                "smooth": [e.__dict__ for e in smooth],
                "align": [e.__dict__ for e in align],
            }
            path = OUT / f"{stem.removeprefix('windows_')}_mix{k}.json"
            path.write_text(json.dumps(doc, separators=(",", ":")) + "\n")
            print(path.name, len(wk), "rows,", len(smooth), "smooth entries,", len(align), "align")


if __name__ == "__main__":
    main()
