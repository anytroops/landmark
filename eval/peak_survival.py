"""How many of a noisy query's triplet peaks the index has, per peak option
(docs/design/triplet-noise.md).

  python -m eval.peak_survival --tracks 200

For the first --tracks val positives: one 10 s excerpt (the eval's seeded
start, moved to a frame boundary and away from the ends), its reference peaks
taken with 160 frames of context on each side as the indexed track sees them,
and its query peaks from the excerpt alone, clean and under each noise (the
eval's val noise draws). Both use the same option. Survival is the share of
query peaks within 1 frame and 1 band of a reference peak; the band shares are
the query peaks in each sixth of the bands, low to high.

Writes eval/results/peak_survival_seed<seed>.{md,json}.
"""

import argparse
import dataclasses
import json
from pathlib import Path

import numpy as np

from eval.run_eval import condition
from prototype import b2
from prototype.audio import DecodeError, decode
from prototype.build_index import machine_spec
from prototype.fma import audio_path

ROOT = Path(__file__).resolve().parent.parent
OPTIONS = {
    "baseline": b2.DEFAULT,
    "A 129 frames, 50th pct": dataclasses.replace(b2.DEFAULT, norm_frames=129, norm_pct=50.0),
    "B 6 slices x 5": dataclasses.replace(b2.DEFAULT, peak_regions=6, region_cap=5),
}
CONDITIONS = ["clean_10s", "white_snr0_10s", "demand_pstation_snr0_10s",
              "demand_pcafeter_snr0_10s", "demand_presto_snr0_10s", "demand_dliving_snr-5_10s",
              "demand_tcar_snr0_10s"]  # fmt: skip
CONTEXT = 160  # frames of the track on each side of the reference excerpt
SLICES = 6


def peaks(spec: np.ndarray, p: b2.Params) -> b2.Peaks:
    if p.norm_frames > 0 or p.peak_regions > 1:
        return b2.robust_peaks(spec, p)
    return b2.find_peaks(spec, p)


def survival(query: b2.Peaks, ref: set[tuple[int, int]]) -> float:
    near = [
        any((t + dt, f + df) in ref for dt in (-1, 0, 1) for df in (-1, 0, 1))
        for t, f in zip(query.t.tolist(), query.f.tolist(), strict=True)
    ]
    return float(np.mean(near)) if near else 0.0


def measure(ids: list[int], subset: str, seed: int) -> dict[str, object]:
    hop = b2.DEFAULT.hop
    n = 10 * 11025
    pad = CONTEXT * hop
    surv = {o: {c: [] for c in CONDITIONS} for o in OPTIONS}
    shares = {o: {c: np.zeros(SLICES) for c in CONDITIONS} for o in OPTIONS}
    used = 0
    for fma_id in ids:
        try:
            pcm = decode(audio_path(fma_id, subset))
        except DecodeError:
            continue
        if len(pcm) < n + 2 * pad:
            continue
        used += 1
        rng = np.random.default_rng([seed, fma_id, 0, 10_000])
        start = int(rng.integers(pad, len(pcm) - n - pad + 1))
        start -= start % hop  # reference and query frames line up
        clip = pcm[start : start + n]
        queries = []
        for ci, name in enumerate(CONDITIONS):
            _, degrade = condition(name, "val")
            queries.append(degrade(clip, np.random.default_rng([seed, fma_id, 0, ci])))
        for o, p in OPTIONS.items():
            rp = peaks(b2.spectrogram(pcm[start - pad : start + n + pad], p), p)
            frames = b2.spectrogram(clip, p).shape[0]
            inside = (rp.t >= CONTEXT) & (rp.t < CONTEXT + frames)
            ref = set(zip((rp.t[inside] - CONTEXT).tolist(), rp.f[inside].tolist(), strict=True))
            for name, q in zip(CONDITIONS, queries, strict=True):
                qp = peaks(b2.spectrogram(q, p), p)
                surv[o][name].append(survival(qp, ref))
                shares[o][name] += np.bincount(qp.f * SLICES // p.n_bands, minlength=SLICES)
    return {
        "tracks": used,
        "survival": {o: {c: float(np.mean(v)) for c, v in d.items()} for o, d in surv.items()},
        "band_shares": {
            o: {c: (v / v.sum()).tolist() for c, v in d.items()} for o, d in shares.items()
        },
    }


def markdown(r: dict[str, object]) -> str:
    opts = list(OPTIONS)
    out = [f"# Triplet peak survival under noise ({r['tracks']} val positives)", "",
           "Share of a query's peaks within 1 frame and 1 band of a peak the indexed track "
           "has at the same place.", "",
           "| Condition | " + " | ".join(opts) + " |", "|---|" + "---|" * len(opts)]  # fmt: skip
    for c in CONDITIONS:
        out.append(
            f"| {c} | " + " | ".join(f"{100 * r['survival'][o][c]:.1f}%" for o in opts) + " |"
        )
    out += ["", "Share of query peaks per sixth of the bands, low to high (%).", "",
            "| Option | Condition | " + " | ".join(str(i + 1) for i in range(SLICES)) + " |",
            "|---|---|" + "---|" * SLICES]  # fmt: skip
    for o in opts:
        for c in CONDITIONS:
            row = " | ".join(f"{100 * x:.1f}" for x in r["band_shares"][o][c])
            out.append(f"| {o} | {c} | {row} |")
    m = r["machine"]
    out += ["", f"Commit {m['commit']}, dirty {m['dirty']}."]
    return "\n".join(out) + "\n"


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--manifest", default=str(ROOT / "eval/manifests/splits_medium_seed0.json"))
    ap.add_argument("--tracks", type=int, default=200)
    ap.add_argument("--seed", type=int, default=0)
    args = ap.parse_args()
    m = json.loads(Path(args.manifest).read_text())
    r = measure(m["val_pos"][: args.tracks], m["meta"]["subset"], args.seed)
    r["machine"] = machine_spec()
    stem = ROOT / "eval" / "results" / f"peak_survival_seed{args.seed}"
    Path(f"{stem}.json").write_text(json.dumps(r, indent=1) + "\n")
    Path(f"{stem}.md").write_text(markdown(r))
    print(markdown(r))


if __name__ == "__main__":
    main()
