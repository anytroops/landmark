"""Front-end experiment for D31 option B2: which spectral front end keeps its
peaks, and triplet hashes built from them, under tempo and pitch changes?

  python -m eval.frontend_experiment --tracks 200

For each test-split track: a 10 s excerpt, its peaks, and the peaks of the
same excerpt after a change (turntable or keylock tempo change, or noise).
Peaks of the changed audio are mapped back to the original's coordinates:
time scales by the tempo factor (both modes); frequency scales by 1/factor
on a linear axis under turntable, shifts by bins_per_octave * log2(factor)
on a log axis, and stays put under keylock. An original peak survives if a
mapped peak lies within 1 frame and 1 bin of it (edges excluded).

Triplets: each peak anchors the next FANOUT peaks within the target zone,
and every pair (j, k) of those targets forms a triplet. The hash keeps only
coordinates that a tempo change cannot move: the time-gap ratio
(t_j - t_i) / (t_k - t_i), quantized, and on a log axis the frequency gaps
in bins (a pitch shift slides all three peaks equally), or on a linear axis
the frequency-gap ratio (f_j - f_i) / (f_k - f_i), quantized (a pitch shift
stretches the gaps). A triplet survives if the changed audio has the same
hash at the mapped anchor time (within 2 frames).

Front ends:
  stft1024   today's: STFT 1024 / hop 256, 512 linear bins (10.8 Hz), 17x17 max filter
  log2048    STFT 2048 / hop 256 into a log filterbank, 36 bands per octave from 55 Hz
  log4096    the same over STFT 4096 (finer low-frequency resolution, longer window)
  cqt36      constant-Q transform, 36 bins per octave from 55 Hz, hop 256 (librosa)
All keep the strongest 30 local maxima per 43-frame block, as today.
"""

import argparse
import json
import os
from dataclasses import dataclass
from multiprocessing import Pool
from pathlib import Path

import librosa
import numpy as np
from numpy.lib.stride_tricks import sliding_window_view
from scipy.ndimage import maximum_filter

from eval.degrade import add_noise, excerpt, keylock, speed
from eval.stats import wilson
from prototype import fingerprint as fp
from prototype.audio import SAMPLE_RATE, DecodeError, decode
from prototype.build_index import machine_spec
from prototype.fma import audio_path

ROOT = Path(__file__).resolve().parent.parent
HOP = 256
FANOUT = 5
BPO = 36
FMIN = 55.0
N_LOG = int(BPO * np.log2(5000 / FMIN))  # up to ~5 kHz, under Nyquist at 11,025 Hz
TIME_LEVELS = 32  # quantization of the time-gap ratio
FREQ_LEVELS = 32  # quantization of the linear frequency-gap ratio


@dataclass(frozen=True)
class FrontEnd:
    name: str
    log_axis: bool
    center: float  # samples from frame start to the window center
    nbhd_f: int
    zone: int  # max |frequency gap| to a target, in bins


FRONTENDS = {
    "stft1024": FrontEnd("stft1024", False, 512, 17, 64),
    "log2048": FrontEnd("log2048", True, 1024, 9, BPO),
    "log4096": FrontEnd("log4096", True, 2048, 9, BPO),
    "cqt36": FrontEnd("cqt36", True, 0, 9, BPO),
}


def log_filterbank(n_fft: int) -> np.ndarray:
    """Triangular filters centered every 1/BPO octave from FMIN, over rfft bins."""
    freqs = np.fft.rfftfreq(n_fft, 1 / SAMPLE_RATE)
    centers = FMIN * 2 ** (np.arange(-1, N_LOG + 1) / BPO)
    fb = np.zeros((N_LOG, len(freqs)))
    for b in range(N_LOG):
        lo, c, hi = centers[b], centers[b + 1], centers[b + 2]
        up = (freqs - lo) / (c - lo)
        down = (hi - freqs) / (hi - c)
        fb[b] = np.clip(np.minimum(up, down), 0, None)
    # Bands narrower than the FFT's bin spacing hold no bin: use the nearest bin.
    empty = fb.sum(axis=1) == 0
    nearest = np.argmin(np.abs(freqs[None, :] - centers[1:-1, None]), axis=1)
    fb[np.nonzero(empty)[0], nearest[empty]] = 1.0
    return fb / fb.sum(axis=1, keepdims=True)


def spectrogram(x: np.ndarray, name: str) -> np.ndarray:
    """dB spectrogram, shape (frames, bins)."""
    if name == "stft1024":
        return fp.spectrogram(x)
    if name == "cqt36":
        c = librosa.cqt(x.astype(np.float32), sr=SAMPLE_RATE, hop_length=HOP, fmin=FMIN,
                        n_bins=N_LOG, bins_per_octave=BPO)  # fmt: skip
        return 20 * np.log10(np.abs(c).T + 1e-10)
    n_fft = 2048 if name == "log2048" else 4096
    if len(x) < n_fft:
        return np.empty((0, N_LOG))
    frames = sliding_window_view(x.astype(np.float64), n_fft)[::HOP]
    mag = np.abs(np.fft.rfft(frames * np.hanning(n_fft), axis=1))
    return 20 * np.log10(mag @ log_filterbank(n_fft).T + 1e-10)


def peaks(spec: np.ndarray, fe: FrontEnd) -> tuple[np.ndarray, np.ndarray]:
    """(t, f) of the strongest 30 local maxima per 43-frame block, floor 60 dB below the max."""
    if spec.size == 0:
        return np.empty(0, int), np.empty(0, int)
    floor = fp.DEFAULT.floor_db if fe.name == "stft1024" else spec.max() - 60
    local = maximum_filter(spec, size=(17, fe.nbhd_f), mode="constant", cval=-np.inf)
    t, f = np.nonzero((spec == local) & (spec > floor))
    mag = spec[t, f]
    block = t // 43
    order = np.lexsort((-mag, block))
    starts = np.searchsorted(block[order], block[order], side="left")
    keep = order[(np.arange(len(order)) - starts) < 30]
    keep = keep[np.lexsort((f[keep], t[keep]))]
    return t[keep], f[keep]


def triplets(t: np.ndarray, f: np.ndarray, fe: FrontEnd) -> tuple[np.ndarray, np.ndarray]:
    """(anchor time, hash) for every triplet; hash packs only tempo-invariant values."""
    out_t, out_h = [], []
    n = len(t)
    for i in range(n):
        tg = []
        for j in range(i + 1, n):
            dt = t[j] - t[i]
            if dt > 63:
                break
            if dt >= 1 and abs(f[j] - f[i]) <= fe.zone:
                tg.append(j)
                if len(tg) == FANOUT:
                    break
        for a in range(len(tg)):
            for b in range(a + 1, len(tg)):
                j, k = tg[a], tg[b]
                if t[k] == t[j]:
                    continue
                rt = int((t[j] - t[i]) / (t[k] - t[i]) * TIME_LEVELS)
                if fe.log_axis:  # gaps in log bins: a pitch shift slides all three
                    a1, a2 = int(f[j] - f[i]) + 128, int(f[k] - f[i]) + 128
                else:  # gap ratio: a pitch shift stretches the gaps equally
                    if f[k] == f[i]:
                        continue
                    rf = (f[j] - f[i]) / (f[k] - f[i])  # quantized over [-4, 4]
                    a1 = (
                        int(np.clip(np.floor(rf * FREQ_LEVELS / 4), -FREQ_LEVELS, FREQ_LEVELS))
                        + 128
                    )
                    a2 = int(np.sign(f[k] - f[i])) + 128
                out_t.append(t[i])
                out_h.append((rt << 16) | (a1 << 8) | a2)
    return np.array(out_t, dtype=float), np.array(out_h, dtype=np.int64)


def map_back(t1: np.ndarray, f1: np.ndarray, fe: FrontEnd, mode: str, factor: float):
    """Coordinates of the changed audio's peaks in the original's frame and bin units."""
    c = fe.center
    t0 = (factor * (t1 * HOP + c) - c) / HOP if mode in ("speed", "keylock") else t1.astype(float)
    if mode == "speed":
        f0 = f1 - BPO * np.log2(factor) if fe.log_axis else f1 / factor
    else:
        f0 = f1.astype(float)
    return t0, f0


def _work(job: tuple) -> list[dict]:
    fma_id, subset, conds, names = job
    try:
        pcm = decode(audio_path(fma_id, subset))
    except DecodeError:
        return []
    rng = np.random.default_rng([0, fma_id])
    try:
        x, _ = excerpt(pcm, 10.0, rng)
    except ValueError:
        return []
    rows = []
    for name in names:
        fe = FRONTENDS[name]
        t0, f0 = peaks(spectrogram(x, name), fe)
        ht0, hh0 = triplets(t0, f0, fe)
        rows.append({"fma_id": fma_id, "frontend": name, "mode": "hashes", "factor": 0.0,
                     "hashes": hh0.tolist()})  # fmt: skip
        for mode, factor in conds:
            if mode == "speed":
                y = speed(x, factor)
            elif mode == "keylock":
                y = keylock(x, factor)
            else:
                y = add_noise(x, factor, np.random.default_rng([1, fma_id]))
            t1, f1 = peaks(spectrogram(y, name), fe)
            mt, mf = map_back(t1, f1, fe, mode, factor if mode != "noise" else 1.0)
            # Only original peaks whose time the changed excerpt also covers, away from edges.
            span = max(mt.max(), 0) if len(mt) else 0
            inner = (t0 >= 9) & (t0 <= min(t0.max(initial=0), span) - 9)
            surv = 0
            for tt, ff in zip(t0[inner], f0[inner], strict=True):
                surv += bool(np.any((np.abs(mt - tt) <= 1) & (np.abs(mf - ff) <= 1)))
            ht1, hh1 = triplets(t1, f1, fe)
            mht = map_back(ht1, np.zeros(len(ht1)), fe, mode, factor if mode != "noise" else 1.0)[0]
            hin = (ht0 >= 9) & (ht0 <= min(t0.max(initial=0), span) - 75)
            lookup: dict[int, list[float]] = {}
            for tt, hh in zip(mht, hh1, strict=True):
                lookup.setdefault(int(hh), []).append(tt)
            hsurv = hnear = 0
            for tt, hh in zip(ht0[hin], hh0[hin], strict=True):
                hit = any(abs(u - tt) <= 2 for u in lookup.get(int(hh), ()))
                hsurv += hit
                # Near: every field within one quantization step (multi-probe lookup).
                if not hit:
                    rt, a1, a2 = hh >> 16, (hh >> 8) & 255, hh & 255
                    d2 = (-1, 0, 1) if fe.log_axis else (0,)  # linear a2 is a sign
                    hit = any(
                        abs(u - tt) <= 2
                        for x in (-1, 0, 1) for y in (-1, 0, 1) for z in d2
                        for u in lookup.get(int(((rt + x) << 16) | ((a1 + y) << 8) | (a2 + z)), ())
                    )  # fmt: skip
                hnear += hit
            rows.append({"fma_id": fma_id, "frontend": name, "mode": mode, "factor": factor,
                         "peaks": int(inner.sum()), "peaks_kept": surv,
                         "triplets": int(hin.sum()), "triplets_kept": hsurv,
                         "triplets_near": hnear,
                         "peaks_per_s": len(t0) / 10.0, "triplets_per_s": len(ht0) / 10.0,
                         "distinct_hashes": len(set(hh0.tolist()))})  # fmt: skip
    return rows


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--manifest", default=str(ROOT / "eval/manifests/splits_medium_seed0.json"))
    ap.add_argument("--tracks", type=int, default=200)
    ap.add_argument("--frontends", nargs="+", default=list(FRONTENDS))
    ap.add_argument("--workers", type=int, default=os.cpu_count())
    args = ap.parse_args()
    manifest = json.loads(Path(args.manifest).read_text())
    ids = sorted(manifest["test_pos"])[: args.tracks]
    conds = [("speed", f) for f in (1.0, 0.92, 0.96, 0.98, 1.02, 1.04, 1.08)]
    conds += [("keylock", f) for f in (0.92, 0.96, 1.04, 1.08)] + [("noise", 10.0)]
    jobs = [(i, manifest["meta"]["subset"], conds, args.frontends) for i in ids]
    rows = []
    with Pool(args.workers) as pool:
        for r in pool.imap_unordered(_work, jobs):
            rows += r
    import pandas as pd

    hashes = [r for r in rows if r["mode"] == "hashes"]
    d = pd.DataFrame([r for r in rows if r["mode"] != "hashes"])
    out = ROOT / "eval" / "results" / "frontend_seed0"
    d.to_csv(out.with_suffix(".csv.gz"), index=False)
    g = d.groupby(["frontend", "mode", "factor"]).sum(numeric_only=True)
    n_tracks = d["fma_id"].nunique()
    lines = [
        "# Front-end experiment: survival under tempo change and noise",
        "",
        f"{n_tracks} fma_medium test tracks, 10 s excerpts; Wilson 95% CIs.",
        "",
        "| Front end | Change | Peaks kept | Triplets kept (exact) | Triplets kept (+/-1 step) |",
        "|---|---|---|---|---|",
    ]
    summary = {}
    for (name, mode, factor), r in g.iterrows():
        pk, pn, tk, tn = (
            int(r["peaks_kept"]),
            int(r["peaks"]),
            int(r["triplets_kept"]),
            int(r["triplets"]),
        )
        plo, phi = wilson(pk, pn)
        tlo, thi = wilson(tk, tn)
        label = f"{mode} {factor:g}" if mode != "noise" else f"noise {factor:g} dB"
        nk = int(r["triplets_near"])
        lines.append(f"| {name} | {label} | {100 * pk / pn:.1f}% ({100 * plo:.1f}-{100 * phi:.1f}) "
                     f"| {100 * tk / tn:.1f}% ({100 * tlo:.1f}-{100 * thi:.1f}) "
                     f"| {100 * nk / tn:.1f}% |")  # fmt: skip
        summary.setdefault(name, {})[label] = {"peaks_kept": pk / pn, "triplets_kept": tk / tn,
                                               "triplets_near": nk / tn}  # fmt: skip
    per = (
        d[d["mode"] == "noise"]
        .groupby("frontend")[["peaks_per_s", "triplets_per_s", "distinct_hashes"]]
        .mean()
    )
    lines += [
        "",
        "| Front end | Peaks per s | Triplets per s | Distinct triplet hashes per 10 s excerpt |",
        "|---|---|---|---|",
    ]
    for name, r in per.iterrows():
        pps, tps, dh = r["peaks_per_s"], r["triplets_per_s"], r["distinct_hashes"]
        lines.append(f"| {name} | {pps:.1f} | {tps:.1f} | {dh:.0f} |")
    # Specificity: how long a query hash's posting list would be at fma_medium
    # scale (23,985 tracks x 30 s), from the hash distribution of the originals:
    # expected list length = total postings x sum of squared hash frequencies.
    lines += [
        "",
        "| Front end | Distinct hashes | Entropy | Postings per query hash at fma_medium "
        "| Postings scanned per 10 s query |",
        "|---|---|---|---|---|",
    ]
    specificity = {}
    for name in sorted({r["frontend"] for r in hashes}):
        h = np.concatenate([np.array(r["hashes"], dtype=np.int64) for r in hashes
                            if r["frontend"] == name])  # fmt: skip
        _, c = np.unique(h, return_counts=True)
        pr = c / c.sum()
        per_s = len(h) / (10.0 * n_tracks)
        per_hash = 23985 * 30 * per_s * float((pr**2).sum())
        specificity[name] = {"distinct": len(c), "entropy_bits": float(-(pr * np.log2(pr)).sum()),
                             "postings_per_query_hash": per_hash,
                             "postings_per_query": per_hash * per_s * 10}  # fmt: skip
        sp = specificity[name]
        lines.append(f"| {name} | {sp['distinct']} | {sp['entropy_bits']:.1f} bits "
                     f"| {per_hash:,.0f} | {sp['postings_per_query'] / 1e6:.1f}M |")  # fmt: skip
    summary["specificity"] = specificity
    out.with_suffix(".md").write_text("\n".join(lines) + "\n")
    out.with_suffix(".json").write_text(
        json.dumps(
            {"tracks": int(d["fma_id"].nunique()), "summary": summary, "machine": machine_spec()},
            indent=1,
        )
        + "\n"
    )
    print("\n".join(lines))


if __name__ == "__main__":
    main()
