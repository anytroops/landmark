"""Where a song's Rekordbox beat grid lands in a recorded set (the lighting
work's timing, docs/design/light-show.md).

  python -m eval.beat_align --verify data/mine/set.d57.json --set set.wav \\
      --index data/segments/mine_b2 --usb ~/Desktop --out eval/results/lights/beat_align_a.json

For each entry that passed the audio check (D57) and whose song Rekordbox
analysed: the grid's offset against the song's own audio (the lag that best
lines the grid up with the song's onsets: decoders and Rekordbox disagree
about where a file starts by tens of milliseconds), then the corrected grid
mapped into the set by the entry's alignment (set seconds = (song seconds - a)
/ rate) and the lag that best lines it up with the set's onsets: the timing
error a light cue on that beat would have. Onsets: positive spectral flux of
a log-magnitude spectrogram, 5.8 ms frames, its 1 s running mean removed.
Writes counts and errors, no song names.
"""

import argparse
import json
from pathlib import Path

import numpy as np

from prototype import anlz
from prototype.audio import SAMPLE_RATE, decode

HOP, N = 64, 1024
FPS = SAMPLE_RATE / HOP
LAGS_MS = np.arange(-150, 151, 2)
MARK = 4.23  # analysis.VerifyMark


def onsets(x: np.ndarray) -> np.ndarray:
    frames = np.lib.stride_tricks.sliding_window_view(x, N)[::HOP]
    logm = np.log1p(100.0 * np.abs(np.fft.rfft(frames * np.hanning(N), axis=1)))
    flux = np.concatenate([[0.0], np.maximum(np.diff(logm, axis=0), 0.0).sum(axis=1)])
    return flux - np.convolve(flux, np.ones(int(FPS)) / int(FPS), "same")


def best_lag(env: np.ndarray, times: np.ndarray) -> int:
    """The lag (ms) that maximizes the mean onset strength at times + lag."""
    scores = []
    for lag in LAGS_MS:
        idx = np.round((times + lag / 1000.0) * FPS - N / 2 / HOP).astype(int)
        idx = idx[(idx >= 0) & (idx < len(env))]
        scores.append(env[idx].mean() if len(idx) else -np.inf)
    return int(LAGS_MS[int(np.argmax(scores))])


def checked_entries(path: Path) -> list[dict]:
    """The entries that passed the audio check, each with its alignment a
    and rate (song seconds = a + rate x set seconds): from cmd/tracklist
    -json -verify (every entry written passed; alignment from the source
    positions) or from eval.verify_set (a, rate, and z per entry)."""
    out = []
    for e in json.loads(path.read_text()):
        if "source_start_s" in e and "a" not in e:
            span = max(e["end_s"] - e["start_s"], 1e-9)
            e["rate"] = (e["source_end_s"] - e["source_start_s"]) / span
            e["a"], e["z"] = e["source_start_s"] - e["rate"] * e["start_s"], e.get("check_z", MARK)
        if e.get("a") is not None and e.get("z") is not None and e["z"] >= MARK:
            out.append(e)
    return out


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--verify", type=Path, required=True, help="tracklist -json -verify output")
    ap.add_argument("--set", type=Path, required=True)
    ap.add_argument("--index", type=Path, required=True)
    ap.add_argument("--usb", type=Path, required=True, help="the folder holding PIONEER")
    ap.add_argument("--out", type=Path, required=True)
    args = ap.parse_args()
    lib = anlz.library(args.usb)
    paths = {
        int(x["dense"]): Path(x["path"])
        for x in json.loads((args.index / "extras.json").read_text())
    }
    mix = onsets(decode(args.set))
    own_cache: dict[int, int] = {}
    rows = []
    for e in checked_entries(args.verify):
        p = paths[e["track"]]
        try:
            key = "/" + str(p.relative_to(args.usb))
        except ValueError:
            continue
        if key not in lib:
            continue
        grid = np.array([b.time_s for b in anlz.beats(lib[key][0].read_bytes())])
        if e["track"] not in own_cache:
            own_cache[e["track"]] = best_lag(onsets(decode(p)), grid)
        own = own_cache[e["track"]]
        in_set = (grid + own / 1000.0 - e["a"]) / e["rate"]
        keep = (in_set >= e["start_s"] + 2) & (in_set <= e["end_s"] - 2)
        if keep.sum() < 16:
            continue
        raw = best_lag(mix, ((grid - e["a"]) / e["rate"])[keep])
        rows.append({"beats": int(keep.sum()), "grid_vs_own_ms": own, "uncorrected_ms": raw,
                     "corrected_ms": best_lag(mix, in_set[keep]),
                     "format": p.suffix.lower()})  # fmt: skip
    err = np.abs([r["corrected_ms"] for r in rows])
    raw = np.abs([r["uncorrected_ms"] for r in rows])
    own = [r["grid_vs_own_ms"] for r in rows]
    result = {
        "entries": len(rows),
        "beats": int(sum(r["beats"] for r in rows)),
        "corrected_abs_ms": {"median": float(np.median(err)),
                             "within_20": float(np.mean(err <= 20)),
                             "within_40": float(np.mean(err <= 40)), "max": float(err.max())},
        "uncorrected_abs_ms": {"median": float(np.median(raw)),
                               "within_40": float(np.mean(raw <= 40))},
        "grid_vs_own_ms": {"median": float(np.median(own)), "min": min(own), "max": max(own)},
        "rows": rows,
    }  # fmt: skip
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(result, indent=1) + "\n")
    print(json.dumps({k: v for k, v in result.items() if k != "rows"}, indent=1))


if __name__ == "__main__":
    main()
