"""Tune B2's time-ratio quantization on UnmixDB's calibration sets (D33).

  python -m eval.b2_tune --distractors 2000

On UnmixDB, B2 identifies fewer windows than the speed grid under sox's
keylock stretch and its distortion effect (EVAL). One suspect is the hash's
time-gap ratio rt = floor(rt_levels * (t_j - t_i) / span): at 64 levels a
typical 20-frame span has bins a third of a frame wide, finer than the
timing jitter a time stretch adds. Fewer levels tolerate more jitter but
make hashes less specific (longer posting lists, more chance matches).

Catalog: the UnmixDB reference excerpts plus --distractors fma_medium tracks
(seeded). Queries: 10 s windows every --hop seconds from the calibration
sets (set222, set275) where one known track plays alone, dual-filterbank
query fingerprints (D35); and one 10 s excerpt from each of --negatives
fma_medium test negatives. Peaks are computed once per track and window;
only the triplets differ between variants. For each variant: windows whose
top candidate is the right track (raw), and the same at thresholds chosen
per variant as the most accepting (t_abs, t_ratio) that accept at most
--max-far of the negatives, plus postings scanned. The test sets (set044,
set123, set230, set281) are not touched.

Writes eval/results/b2_tune_seed0<tag>.{csv.gz,md}.
"""

import argparse
import json
import multiprocessing as mp
import os
from dataclasses import replace
from pathlib import Path

import numpy as np
import pandas as pd
import soundfile as sf

from eval.stats import wilson
from prototype import b2, b2_match
from prototype.audio import DecodeError, decode
from prototype.fingerprint import find_peaks
from prototype.fma import audio_path

ROOT = Path(__file__).resolve().parent.parent
RT_LEVELS = (64, 32, 16)
CALIBRATION_SETS = {"set222", "set275"}
T_ABS = (2, 3, 4, 5, 6, 8, 10, 12, 15, 20, 25, 30, 40, 50, 60, 80, 100)
T_RATIO = (1.5, 2.0, 2.5, 3.0, 4.0)

_STATE: dict = {}


def peak_sets(pcm: np.ndarray, query: bool, shifts: int = 4) -> list[tuple]:
    """Peaks (t, f) per filterbank and shift: one set for an index track; for
    a query, both filterbanks (D35) times shifts sub-hop offsets (D15)."""
    p = b2.DEFAULT
    if not query:
        pk = find_peaks(b2.spectrogram(pcm, p), p)
        return [(pk.t, pk.f)]
    step = p.hop // shifts
    out = []
    for q in (p, b2.half_band(p)):
        for k in range(shifts):
            pk = find_peaks(b2.spectrogram(pcm[k * step :], q), q)
            out.append((pk.t, pk.f))
    return out


def landmarks(peaks: list[tuple], rt_levels: int) -> b2.Landmarks:
    p = replace(b2.DEFAULT, rt_levels=rt_levels)
    parts = [b2.triplets(t, f, p) for t, f in peaks]
    if len(parts) == 1:
        return parts[0]
    u64 = np.uint64
    packed = np.unique(np.concatenate([
        (x.hash.astype(u64) << u64(32)) | (x.t.astype(u64) << u64(16))
        | (x.span.astype(u64) << u64(8)) | x.band.astype(u64) for x in parts
    ]))  # fmt: skip
    return b2.Landmarks((packed >> u64(32)).astype(np.uint32),
                        ((packed >> u64(16)) & u64(0xFFFF)).astype(np.uint32),
                        ((packed >> u64(8)) & u64(0xFF)).astype(np.uint8),
                        (packed & u64(0xFF)).astype(np.uint8))  # fmt: skip


def _index_peaks(path: str) -> list[tuple] | None:
    try:
        return peak_sets(decode(Path(path)), query=False)
    except DecodeError:
        return None


def _query(job: tuple) -> list[dict]:
    kind, key, pcm, want, info = job
    peaks = peak_sets(pcm, query=True)
    rows = []
    for rt in RT_LEVELS:
        lm = landmarks(peaks, rt)
        r = b2_match.search(_STATE[rt], lm)
        c = r.candidates
        top = c[0].track if c else -1
        runner = next((x.score for x in c[1:] if x.track != top), 0) if c else 0
        rows.append({"kind": kind, "key": key, "rt_levels": rt, "want": want, "top1": top,
                     "score": c[0].score if c else 0, "runner_up": runner,
                     "postings_scanned": r.postings_scanned, **info})  # fmt: skip
    return rows


def windows(gt: dict, hop: float, win: float = 10.0) -> list[tuple]:
    """Query jobs for calibration-set windows where one known track plays alone.
    References are this catalog's first tracks, in the order their dense ids
    (from dense_offset) number them."""
    jobs = []
    for k, m in enumerate(gt["mixes"]):
        if m["set"] not in CALIBRATION_SETS:
            continue
        audio, sr = sf.read(ROOT / m["file"], dtype="float32")
        for t0 in np.arange(0, len(audio) / sr - win + 1e-9, hop):
            alone = [s for s in m["segments"] if s["known"] and not s["ignore"]
                     and s["mix_start"] + s["fade_in"] <= t0
                     and t0 + win <= s["mix_end"] - s["fade_out"]]  # fmt: skip
            if not alone:
                continue
            a = int(round(t0 * sr))
            info = {"timescale": m["timescale"], "effect": m["effect"], "pct": alone[0]["pct"]}
            jobs.append(("window", f"{k}:{t0:.1f}", audio[a : a + int(win * sr)],
                         alone[0]["dense"] - gt["dense_offset"], info))  # fmt: skip
    return jobs


def accepted(d: pd.DataFrame, ta: float, tr: float) -> pd.Series:
    return (d["top1"] >= 0) & (d["score"] >= ta) & (d["score"] >= tr * d["runner_up"].clip(lower=1))


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--manifest", default=str(ROOT / "eval/manifests/splits_medium_seed0.json"))
    ap.add_argument("--mixes", default="unmixdb_s8")
    ap.add_argument("--refs", default=str(ROOT / "data/unmixdb/unmixdb_s8_refs.tsv"))
    ap.add_argument("--distractors", type=int, default=2000)
    ap.add_argument("--negatives", type=int, default=300)
    ap.add_argument("--hop", type=float, default=5.0)
    ap.add_argument("--max-far", type=float, default=0.01)
    ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--workers", type=int, default=os.cpu_count())
    ap.add_argument("--tag", default="")
    args = ap.parse_args()
    m = json.loads(Path(args.manifest).read_text())
    gt = json.loads((ROOT / "eval" / "mixes" / f"{args.mixes}.json").read_text())
    rng = np.random.default_rng(args.seed)
    refs = [line.split("\t") for line in Path(args.refs).read_text().splitlines() if line.strip()]
    distract = sorted(int(x) for x in rng.choice(m["indexed"], args.distractors, replace=False))
    paths = [p.strip() for _, p in refs] + [str(audio_path(f, "medium")) for f in distract]
    ctx = mp.get_context("fork")
    with ctx.Pool(args.workers) as pool:
        peaks = pool.map(_index_peaks, paths, chunksize=8)
    ok = [p for p in peaks if p is not None]
    print(f"catalog {len(ok)} tracks ({len(refs)} references)", flush=True)
    for rt in RT_LEVELS:
        _STATE[rt] = b2_match.build([landmarks(p, rt) if p else b2.triplets(
            np.empty(0, np.int64), np.empty(0, np.int64)) for p in peaks])  # fmt: skip
        print(f"rt_levels {rt}: {_STATE[rt].n_postings} postings, "
              f"{len(_STATE[rt].keys)} keys", flush=True)  # fmt: skip

    jobs = windows(gt, args.hop)
    neg = sorted(int(x) for x in rng.choice(m["test_neg"], args.negatives, replace=False))
    for f in neg:
        try:
            pcm = decode(audio_path(f, "medium"))
        except DecodeError:
            continue
        a = int(np.random.default_rng([args.seed, f]).integers(0, max(len(pcm) - 110250, 1)))
        jobs.append(("negative", str(f), pcm[a : a + 110250], -1,
                     {"timescale": "", "effect": "", "pct": 0.0}))  # fmt: skip
    print(f"{len(jobs)} queries", flush=True)
    rows = []
    with ctx.Pool(args.workers) as pool:
        for i, r in enumerate(pool.imap_unordered(_query, jobs, chunksize=4), 1):
            rows += r
            if i % 500 == 0:
                print(f"{i}/{len(jobs)} queries", flush=True)
    d = pd.DataFrame(rows)
    out = ROOT / "eval" / "results" / f"b2_tune_seed{args.seed}{args.tag}"
    d.to_csv(out.with_suffix(".csv.gz"), index=False)

    lines = [f"# B2 time-ratio levels on UnmixDB calibration sets (set222, set275): "
             f"{len(ok)}-track catalog", "",
             f"Windows where one known track plays alone, every {args.hop:g} s; thresholds per "
             f"variant: the most accepting with at most {100 * args.max_far:g}% of "
             f"{args.negatives} negative excerpts accepted.", ""]  # fmt: skip
    summary = {}
    for rt in RT_LEVELS:
        x = d[d["rt_levels"] == rt]
        win, neg_rows = x[x["kind"] == "window"].copy(), x[x["kind"] == "negative"]
        win["hit"] = win["top1"] == win["want"]
        best = None
        for ta in T_ABS:
            for tr in T_RATIO:
                if accepted(neg_rows, ta, tr).mean() > args.max_far:
                    continue
                acc = (accepted(win, ta, tr) & win["hit"]).mean()
                if best is None or acc > best[0]:
                    best = (acc, ta, tr)
        if best is None:
            raise SystemExit(f"rt_levels {rt}: no threshold meets the FAR target")
        _, ta, tr = best
        win["acc"] = accepted(win, ta, tr) & win["hit"]
        summary[rt] = (win, ta, tr, accepted(neg_rows, ta, tr).mean())
    lines += ["| | " + " | ".join(f"rt_levels {rt}" for rt in RT_LEVELS) + " |",
              "|---|" + "---|" * len(RT_LEVELS)]  # fmt: skip

    def cell(w: pd.DataFrame, col: str) -> str:
        k, n = int(w[col].sum()), len(w)
        lo, hi = wilson(k, n)
        return f"{100 * k / max(n, 1):.1f} ({100 * lo:.1f}-{100 * hi:.1f})"

    for label, col in (("windows right, raw", "hit"), ("windows right at threshold", "acc")):
        lines.append(f"| {label} | " + " | ".join(cell(summary[rt][0], col) for rt in RT_LEVELS)
                     + " |")  # fmt: skip
    for ts in ("none", "resample", "stretch"):
        for eff in ("none", "bass", "compressor", "distortion"):
            cells = []
            for rt in RT_LEVELS:
                w = summary[rt][0]
                cells.append(cell(w[(w["timescale"] == ts) & (w["effect"] == eff)], "acc"))
            lines.append(f"| {ts} / {eff}, at threshold | " + " | ".join(cells) + " |")
    lines.append("| thresholds (t_abs, t_ratio), negatives accepted | " + " | ".join(
        f"{summary[rt][1]}, {summary[rt][2]}; {100 * summary[rt][3]:.2f}%" for rt in RT_LEVELS)
        + " |")  # fmt: skip
    lines.append("| median postings scanned per window | " + " | ".join(
        f"{d[(d['rt_levels'] == rt) & (d['kind'] == 'window')]['postings_scanned'].median():,.0f}"
        for rt in RT_LEVELS) + " |")  # fmt: skip
    lines.append("| median right-track score | " + " | ".join(
        f"{summary[rt][0].loc[summary[rt][0]['hit'], 'score'].median():.0f}" for rt in RT_LEVELS)
        + " |")  # fmt: skip
    out.with_suffix(".md").write_text("\n".join(lines) + "\n")
    print("\n".join(lines))


if __name__ == "__main__":
    main()
