"""The mixer-moves model on recorded sets without labels
(docs/design/mixer-moves.md, evaluation 3): how well each method's gains
explain the set's audio.

  python -m eval.mixer_sets --tag lr1e-3 --index data/segments/mine_b2 \\
      --set pitch:data/mine/pitch.d57.json:"$HOME/Desktop/DJ Sets/...wav" ...

For every entry of a set's verified tracklist (cmd/tracklist -json -verify),
its reference is warped onto the set by the entry's alignment (from its
source positions: source time = a + rate x set time), turntable or keylock
by whichever fits the set better (as D43), over the entry and 10 s either
side where the warped reference has power. Then B0 (D43's broadband gain),
B1 (per-band NNLS), and the model each give gains, and the set's power is
rebuilt from them (prototype.mixer.reconstruction_error). Writes only
aggregates, eval/results/mixer/sets_<tag>.json: no song names.
"""

import argparse
import json
from pathlib import Path

import numpy as np

from eval.dj_analysis import FPS, MARGIN, power_spectrogram, warp
from eval.mixer_train import RES, broadband_b0, load_model
from prototype import mixer
from prototype.audio import decode
from prototype.mixer_model import song_gains

SPAN_S = 10.0
PAD = 400  # frames of context around each chunk: more than the model's receptive field


def sources_for(
    mix: np.ndarray, entries: list[dict], paths: dict[int, Path]
) -> list[tuple[int, np.ndarray]]:
    """Each entry's warped reference as (first frame, frames x bands), over
    the frames it may play: a whole set's worth of full-length arrays would
    not fit in memory."""
    n = len(mix)
    out = []
    for e in entries:
        if e.get("source_start_s") is None or e["track"] not in paths:
            continue
        rate = (e["source_end_s"] - e["source_start_s"]) / max(e["end_s"] - e["start_s"], 1e-9)
        lo = max(0, int((e["start_s"] - SPAN_S) * FPS))
        hi = min(n, int((e["end_s"] + SPAN_S) * FPS))
        a = e["source_start_s"] - rate * e["start_s"] + rate * lo / FPS  # aligned to frame lo
        ref = power_spectrogram(decode(paths[e["track"]]))
        core = slice(int(e["start_s"] * FPS) - lo, int(min(e["end_s"] * FPS, n)) - lo)
        best = None
        for pitch in (False, True):
            w = warp(ref, a, rate, hi - lo, pitch)
            m, s = mix[lo:hi][core, MARGIN:-MARGIN], w[core, MARGIN:-MARGIN]
            scale = np.sum(m * s) / max(np.sum(s * s), 1e-20)
            err = float(np.mean((np.log10(m + 1e-12) - np.log10(scale * s + 1e-12)) ** 2))
            if best is None or err < best[0]:
                best = (err, w)
        w = best[1]
        power = w.sum(axis=1)
        typical = np.median(power[power > 0]) if (power > 0).any() else 0.0
        w[power <= 0.05 * typical] = 0.0
        out.append((lo, w))
    return out


def chunks(n: int, sources: list[tuple[int, np.ndarray]]) -> list[tuple[int, int]]:
    """The set cut where at most one song plays (in the middle of each such
    stretch longer than 2 PAD), so no chunk splits a transition."""
    count = np.zeros(n, int)
    for lo, w in sources:
        count[lo : lo + len(w)] += w.sum(axis=1) > 0
    cuts, i = [0], 0
    while i < n:
        if count[i] <= 1:
            j = i
            while j < n and count[j] <= 1:
                j += 1
            if j - i > 2 * PAD and 0 < i and j < n:
                cuts.append((i + j) // 2)
            i = j
        else:
            i += 1
    return list(zip(cuts, cuts[1:] + [n], strict=True))


def errors(mix: np.ndarray, sources: list[tuple[int, np.ndarray]], model) -> dict[str, float]:
    """Each method's reconstruction error over the whole set, chunk by chunk."""
    sums: dict[str, float] = {}
    frames = 0
    cols = np.r_[mixer.BAND_EDGES[0] : mixer.BAND_EDGES[-1]]
    for c0, c1 in chunks(len(mix), sources):
        p0, p1 = max(0, c0 - PAD), min(len(mix), c1 + PAD)
        part = []
        for lo, w in sources:
            a, b = max(lo, p0), min(lo + len(w), p1)
            if b > a:
                full = np.zeros((p1 - p0, mix.shape[1]))
                full[a - p0 : b - p0] = w[a - lo : b - lo]
                part.append(full)
        if not part:
            continue
        m = mix[p0:p1]
        b1 = mixer.fit_b1(m, part)
        gains = {"b0": broadband_b0(m, part), "b1": b1, "model": song_gains(model, m, part, b1)}
        core = np.zeros(p1 - p0, bool)
        core[c0 - p0 : c1 - p0] = True
        core &= np.any(np.stack([s.sum(axis=1) > 0 for s in part], axis=1), axis=1)
        frames += int(core.sum())
        for k, g in gains.items():
            rebuilt = np.zeros_like(m)
            for j, s in enumerate(part):
                for band, sl in enumerate(mixer.band_slices()):
                    rebuilt[:, sl] += (g[j, :, band] ** 2)[:, None] * s[:, sl]
            d = np.log10(m[core][:, cols] + 1e-10) - np.log10(rebuilt[core][:, cols] + 1e-10)
            sums[k] = sums.get(k, 0.0) + float(np.sum(d**2))
    return {k: round(v / (frames * len(cols)), 4) for k, v in sums.items()}


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--tag", required=True)
    ap.add_argument("--index", type=Path, required=True)
    ap.add_argument("--set", action="append", required=True,
                    help="name:entries.json:set.wav (the name labels the output)")  # fmt: skip
    args = ap.parse_args()
    paths = {int(x["dense"]): Path(x["path"])
             for x in json.loads((args.index / "extras.json").read_text())}  # fmt: skip
    model = load_model(args.tag)
    result = {"tag": args.tag, "sets": {}}
    for spec in args.set:
        name, entries_path, audio = spec.split(":", 2)
        mix = power_spectrogram(decode(Path(audio)))
        sources = sources_for(mix, json.loads(Path(entries_path).read_text()), paths)
        result["sets"][name] = {"songs": len(sources), "minutes": round(len(mix) / FPS / 60, 1),
                                "reconstruction_error": errors(mix, sources, model)}  # fmt: skip
        print(name, json.dumps(result["sets"][name]), flush=True)
    RES.mkdir(parents=True, exist_ok=True)
    (RES / f"sets_{args.tag}.json").write_text(json.dumps(result, indent=1) + "\n")


if __name__ == "__main__":
    main()
