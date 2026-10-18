"""Turntable scores of B2 dip at half-band pitch shifts: compare fixes (D33).

  python -m eval.b2_bandshift --positives 300 --negatives 200 --extra 1000

A turntable change of s percent slides every peak by 36 * log2(1 + s/100)
log bands. Peaks round to whole bands, so near a half-band shift the band
differences in the hash flip between neighbors and the true track's score
falls (EVAL, B2 vs pairs). Variants:

- base:   today's B2.
- dual:   the query is also fingerprinted on a filterbank offset by half a
          band (fmin * 2^(1/72)) and the two landmark sets are unioned; the
          index is unchanged. About 2x query fingerprinting and lookups.
- interp: each peak's band position is refined by a parabola through its
          band and the two neighbors (dB), and the hash takes the rounded
          difference of refined positions, which a pitch shift leaves
          unchanged. Index and query both use it; no extra query cost.

Catalog: --positives test positives plus --extra other indexed fma_medium
tracks (seeded). Queries: one 10 s excerpt per positive and negative per
condition (turntable -8% .. +8% in 0.5% steps, keylock +/-4% and +/-8%,
white noise at SNR 5 and 0 dB, clean), the same seeded excerpts and
degradations as eval.run_eval. Each variant's rows use the thresholds B2
was calibrated to on val in eval.b2_eval (t_abs 2, t_ratio 2.5), not tuned
here.

Writes eval/results/b2_bandshift_seed0<tag>.{csv.gz,md}.
"""

import argparse
import json
import multiprocessing as mp
import os
from dataclasses import replace
from pathlib import Path

import numpy as np
import pandas as pd

from eval.run_eval import condition
from eval.stats import wilson
from prototype import b2, b2_match
from prototype.audio import DecodeError, decode
from prototype.fingerprint import find_peaks
from prototype.fma import audio_path

ROOT = Path(__file__).resolve().parent.parent
VARIANTS = ("base", "dual", "interp")
T_ABS, T_RATIO = 2, 2.5  # eval.b2_eval's val calibration for B2
HALF_BAND = replace(b2.DEFAULT, fmin=b2.DEFAULT.fmin * 2 ** (1 / (2 * b2.DEFAULT.bands_per_octave)))


def conditions() -> list[str]:
    pcts = [p / 2 for p in range(-16, 17) if p != 0]
    out = ["clean_10s", "white_snr5_10s", "white_snr0_10s"]
    out += [f"speed_{p:+g}pct_10s" for p in pcts]
    out += [f"keylock_{p:+d}pct_10s" for p in (-8, -4, 4, 8)]
    return out


def refined_positions(spec: np.ndarray, t: np.ndarray, f: np.ndarray) -> np.ndarray:
    """Peak band positions refined by a parabola through bands f-1, f, f+1."""
    inner = (f > 0) & (f < spec.shape[1] - 1)
    fl, fr = np.where(inner, f - 1, f), np.where(inner, f + 1, f)
    a, b, c = spec[t, fl], spec[t, f], spec[t, fr]
    den = a - 2 * b + c
    with np.errstate(divide="ignore", invalid="ignore"):
        delta = np.where(inner & (den < 0), 0.5 * (a - c) / den, 0.0)
    return f + np.clip(delta, -0.5, 0.5)


def fingerprint_interp(pcm: np.ndarray, p: b2.Params = b2.DEFAULT) -> b2.Landmarks:
    spec = b2.spectrogram(pcm, p)
    pk = find_peaks(spec, p)
    return b2.triplets(pk.t, pk.f, p, fpos=refined_positions(spec, pk.t, pk.f))


def union(parts: list[b2.Landmarks]) -> b2.Landmarks:
    """Landmarks of all parts without exact duplicates, as fingerprint_query."""
    u64 = np.uint64
    packed = np.unique(np.concatenate([
        (x.hash.astype(u64) << u64(32)) | (x.t.astype(u64) << u64(16))
        | (x.span.astype(u64) << u64(8)) | x.band.astype(u64) for x in parts
    ]))  # fmt: skip
    return b2.Landmarks((packed >> u64(32)).astype(np.uint32),
                        ((packed >> u64(16)) & u64(0xFFFF)).astype(np.uint32),
                        ((packed >> u64(8)) & u64(0xFF)).astype(np.uint8),
                        (packed & u64(0xFF)).astype(np.uint8))  # fmt: skip


def query_landmarks(q: np.ndarray, variant: str, shifts: int = 4) -> b2.Landmarks:
    if variant == "base":
        return b2.fingerprint_query(q, shifts)
    if variant == "dual":
        return union([b2.fingerprint_query(q, shifts), b2.fingerprint_query(q, shifts, HALF_BAND)])
    step = b2.DEFAULT.hop // shifts
    return union([fingerprint_interp(q[k * step :]) for k in range(shifts)])


_STATE: dict = {}


def _fingerprint(fma_id: int) -> tuple:
    try:
        pcm = decode(audio_path(fma_id, _STATE["subset"]))
    except DecodeError:
        return fma_id, None, None
    return fma_id, b2.fingerprint(pcm), fingerprint_interp(pcm)


def _query(job: tuple) -> list[dict]:
    label, fma_id, seed, conds = job
    try:
        pcm = decode(audio_path(fma_id, _STATE["subset"]))
    except DecodeError:
        return []
    want, cluster = _STATE["dense"].get(fma_id, -1), _STATE["cluster"]
    rows = []
    for ci, name in enumerate(conds):
        secs, degrade = condition(name)
        rng = np.random.default_rng([seed, fma_id, 0, int(secs * 1000)])
        n = int(round(secs * 11025))
        if len(pcm) < n:
            continue
        start = int(rng.integers(0, len(pcm) - n + 1))
        q = degrade(pcm[start : start + n], np.random.default_rng([seed, fma_id, 0, ci]))
        for variant in VARIANTS:
            index = _STATE["interp" if variant == "interp" else "base"]
            lm = query_landmarks(q, variant)
            r = b2_match.search(index, lm)
            c = r.candidates
            top = c[0].track if c else -1
            runner = (
                next((x.score for x in c[1:] if cluster[x.track] != cluster[top]), 0) if c else 0
            )
            rows.append({"label": label, "fma_id": fma_id, "cond": name, "variant": variant,
                         "top1_dense": top, "score": c[0].score if c else 0,
                         "runner_up": runner, "scale": c[0].scale if c else 0.0,
                         "n_landmarks": len(lm.hash), "postings_scanned": r.postings_scanned,
                         "correct": bool(want >= 0 and top >= 0
                                         and cluster[top] == cluster[want])})  # fmt: skip
    return rows


def accepted(d: pd.DataFrame) -> pd.Series:
    return (
        (d["top1_dense"] >= 0)
        & (d["score"] >= T_ABS)
        & (d["score"] >= T_RATIO * d["runner_up"].clip(lower=1))
    )


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--manifest", default=str(ROOT / "eval/manifests/splits_medium_seed0.json"))
    ap.add_argument("--positives", type=int, default=300)
    ap.add_argument("--negatives", type=int, default=200)
    ap.add_argument("--extra", type=int, default=1000)
    ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--workers", type=int, default=os.cpu_count())
    ap.add_argument("--tag", default="")
    args = ap.parse_args()
    m = json.loads(Path(args.manifest).read_text())
    rng = np.random.default_rng(args.seed)
    pos = sorted(int(x) for x in rng.choice(m["test_pos"], args.positives, replace=False))
    neg = sorted(int(x) for x in rng.choice(m["test_neg"], args.negatives, replace=False))
    others = sorted(set(m["indexed"]) - set(m["test_pos"]) - set(m["val_pos"]))
    ids = sorted(set(pos) | {int(x) for x in rng.choice(others, args.extra, replace=False)})
    _STATE["subset"] = m["meta"]["subset"]
    ctx = mp.get_context("fork")
    with ctx.Pool(args.workers) as pool:
        fps = [r for r in pool.map(_fingerprint, ids, chunksize=8) if r[1] is not None]
    ids = [f for f, _, _ in fps]
    _STATE["dense"] = dense = {f: i for i, f in enumerate(ids)}
    cluster = np.arange(len(ids))
    for members in m["clusters"].values():  # duplicates count as the same track (D12, D14)
        ds = [dense[f] for f in members if f in dense]
        if ds:
            cluster[ds] = min(ds)
    _STATE["cluster"] = cluster
    _STATE["base"] = b2_match.build([x for _, x, _ in fps])
    _STATE["interp"] = b2_match.build([x for _, _, x in fps])
    print(f"catalog {len(ids)} tracks: base {_STATE['base'].n_postings} postings, "
          f"interp {_STATE['interp'].n_postings}", flush=True)  # fmt: skip
    conds = conditions()
    jobs = [("pos", f, args.seed, conds) for f in pos] + [("neg", f, args.seed, conds) for f in neg]
    rows = []
    with ctx.Pool(args.workers) as pool:
        for i, r in enumerate(pool.imap_unordered(_query, jobs, chunksize=2), 1):
            rows += r
            if i % 50 == 0:
                print(f"{i}/{len(jobs)} query tracks", flush=True)
    d = pd.DataFrame(rows)
    out = ROOT / "eval" / "results" / f"b2_bandshift_seed{args.seed}{args.tag}"
    d.to_csv(out.with_suffix(".csv.gz"), index=False)

    lines = [f"# B2 half-band dips: {len(ids)}-track catalog, {len(pos)} positives, "
             f"{len(neg)} negatives", "",
             f"Accuracy at t_abs {T_ABS}, t_ratio {T_RATIO} (95% Wilson CI) "
             "/ median true-track score.",
             "", "| Condition | " + " | ".join(VARIANTS) + " |",
             "|---|" + "---|" * len(VARIANTS)]  # fmt: skip
    p = d[d["label"] == "pos"]
    for cond in conds:
        cells = []
        for v in VARIANTS:
            x = p[(p["cond"] == cond) & (p["variant"] == v)]
            k = int((accepted(x) & x["correct"]).sum())
            lo, hi = wilson(k, len(x))
            med = x.loc[x["correct"], "score"].median()
            cells.append(
                f"{100 * k / max(len(x), 1):.1f} ({100 * lo:.1f}-{100 * hi:.1f}) / {med:.0f}"
            )
        lines.append(f"| {cond} | " + " | ".join(cells) + " |")
    n = d[d["label"] == "neg"]
    lines.append("")
    for v in VARIANTS:
        x, y = n[n["variant"] == v], p[p["variant"] == v]
        lines.append(f"{v}: FAR {100 * accepted(x).mean():.2f}% over {len(x)} negative queries, "
                     f"median landmarks per query {y['n_landmarks'].median():.0f}, "
                     f"median postings scanned {y['postings_scanned'].median():,.0f}")  # fmt: skip
    out.with_suffix(".md").write_text("\n".join(lines) + "\n")
    print("\n".join(lines))


if __name__ == "__main__":
    main()
