"""DJ mix analysis prototype (D43): gain curves, transitions, tempo.

  python -m eval.dj_analysis --sets set044 --out eval/results/dj_analysis/set044

For each mix, the tracklist (the hybrid's calibrated smoothing decoder over
the eval's window rows, as cmd/tracklist) names the songs; each song's
alignment to the mix (reference time = a + rate * mix time) comes from its
triplet line fits. Each song's reference audio is warped onto the mix's
timeline on the triplet front end's log-frequency axis: time by the
alignment, and pitch by 36 log2(rate) bands when the song was sped up like
a turntable (keylock keeps pitch; both are tried and the better fit kept).
Then, frame by frame, the mix's band powers are explained as a
non-negative combination of the songs playing there:

    P_mix(band, t) ~ sum_s g_s(t)^2 P_s(band, t)

and g_s(t) is song s's gain (amplitude). Gains are normalized by their
median where the song plays alone and smoothed over 1 s.

Checked against UnmixDB's labels (linear crossfades): each song's fade-in
and fade-out, and the gain curve's error against the labeled ramp. Two fade
estimators are kept: where the gain crosses 10% and 90% of its level, and
the linear ramp that fits the gain best (least squares). On the
calibration sets (set222, set275) the ramp fit had the steadier tails (90th
percentile error lower on 3 of 4 fade edges, medians within 0.5 s) and is
the one reported. The baseline is the window decoder alone: a song's
fade-in spans its entry's first window, its fade-out the last. Developed
on the calibration sets only; the test sets were scored once.
"""

import argparse
import json
from multiprocessing import Pool
from pathlib import Path

import numpy as np
import pandas as pd
from scipy.ndimage import median_filter, uniform_filter1d
from scipy.optimize import nnls

from eval.tracklist_score import RES, ROOT, combine, decode_smooth, merge_repeats, pair_rows
from eval.unmixdb import parse_labels
from prototype import b2
from prototype.audio import SAMPLE_RATE, decode

FPS = SAMPLE_RATE / b2.DEFAULT.hop  # spectrogram frames per second
# Bands left out at each end of the axis when comparing or fitting: a pitch
# shift moves content across the edges, and the lowest bands hold much of a
# club track's power. 8 bands is a +/-16% turntable change.
MARGIN = 8
WINDOW_S = 10.0
CALIBRATION = RES / "calibration_unmixdb_s8_b2dhp_hybrid_set222-set275.json"


def power_spectrogram(pcm: np.ndarray) -> np.ndarray:
    """Band powers on the triplet front end's log-frequency axis, (frames, bands)."""
    return 10 ** (b2.spectrogram(pcm) / 10)


def alignment(
    rows: pd.DataFrame, track: int, start: float, end: float
) -> tuple[float, float] | None:
    """(a, rate) with reference seconds = a + rate * mix seconds, from the
    song's triplet rows (window as is) inside its entry: the median rate,
    then the median intercept."""
    near = (rows["t0"] >= start - WINDOW_S) & (rows["t0"] <= end)
    r = rows[(rows["mode"] == "b2") & (rows["top1_dense"] == track) & near]
    if len(r) == 0:
        return None
    rate = float(np.median(r["factor"]))
    a = float(np.median(r["offset"] / FPS - rate * r["t0"]))
    return a, rate


def warp(ref: np.ndarray, a: float, rate: float, n_frames: int, pitch: bool) -> np.ndarray:
    """The reference's band powers at each mix frame: time warped by the
    alignment (linear interpolation; zero outside the reference) and, if
    pitch, shifted up by the turntable's 36 log2(rate) bands."""
    t = np.arange(n_frames) / FPS
    pos = (a + rate * t) * FPS
    i0 = np.floor(pos).astype(int)
    w = (pos - i0)[:, None]
    inside = (i0 >= 0) & (i0 + 1 < len(ref))
    out = np.zeros((n_frames, ref.shape[1]))
    out[inside] = (1 - w[inside]) * ref[i0[inside]] + w[inside] * ref[i0[inside] + 1]
    if pitch:
        shift = b2.DEFAULT.bands_per_octave * np.log2(rate)
        bands = np.arange(ref.shape[1]) - shift
        out = np.array(
            [np.interp(bands, np.arange(ref.shape[1]), f) for f in out]
        )  # clamps at the edges
    return out


def gains(mix: np.ndarray, sources: list[np.ndarray], active: np.ndarray) -> np.ndarray:
    """Per frame, non-negative least squares of the mix's band powers on the
    active sources' (active: frames x sources); returns amplitudes."""
    g = np.zeros((len(mix), len(sources)))
    for t in range(len(mix)):
        idx = np.nonzero(active[t])[0]
        if len(idx) == 0:
            continue
        a = np.stack([sources[s][t, MARGIN:-MARGIN] for s in idx], axis=1)
        coef, _ = nnls(a, mix[t, MARGIN:-MARGIN])
        g[t, idx] = np.sqrt(coef)
    return g


def fit_ramp(
    x: np.ndarray, t: np.ndarray, rising: bool, lo: float, hi: float
) -> tuple[float, float] | None:
    """The linear fade (0 to 1 if rising, 1 to 0 if not, flat outside) that
    best fits the normalized gain x by least squares, its start and end
    searched on a 0.1 s grid within [lo, hi]; None if x has no frames there."""
    sel = (t >= lo) & (t <= hi)
    if sel.sum() < 2:
        return None
    ts, xs = t[sel], np.clip(x[sel], 0, 1.5)
    grid = np.arange(lo, hi + 1e-9, 0.1)
    best = None
    for i, a in enumerate(grid):
        for b in grid[i:]:
            ramp = np.clip((ts - a) / max(b - a, 1e-9), 0, 1)
            if not rising:
                ramp = 1 - ramp
            err = float(np.sum((xs - ramp) ** 2))
            if best is None or err < best[0]:
                best = (err, a, b)
    return best[1], best[2]


def crossings(g: np.ndarray, level: float, rising: bool) -> tuple[float, float] | None:
    """Times where a normalized gain curve passes 10% and 90% of level,
    rising (fade-in: first crossings) or falling (fade-out: last)."""
    x = g / level
    if rising:
        lo, hi = np.argmax(x >= 0.1), np.argmax(x >= 0.9)
        return (lo / FPS, hi / FPS) if x[hi] >= 0.9 else None
    lo = len(x) - 1 - np.argmax(x[::-1] >= 0.1)
    hi = len(x) - 1 - np.argmax(x[::-1] >= 0.9)
    return (hi / FPS, lo / FPS) if x[hi] >= 0.9 else None


def fade_in_from(c: tuple[float, float]) -> tuple[float, float]:
    """A linear fade-in crossing 10% at t10 and 90% at t90 runs from
    t10 - 0.1 span to t90 + 0.1 span, span = (t90 - t10) / 0.8."""
    t10, t90 = c
    span = (t90 - t10) / 0.8
    return t10 - 0.1 * span, t90 + 0.1 * span


def fade_out_from(c: tuple[float, float]) -> tuple[float, float]:
    """A linear fade-out crossing 90% at t90 and 10% at t10 (later)."""
    t90, t10 = c
    span = (t10 - t90) / 0.8
    return t90 - 0.1 * span, t10 + 0.1 * span


def analyze(job: tuple[int, dict, pd.DataFrame, dict, dict]) -> list[dict]:
    i, mix, rows, params, paths = job
    entries = merge_repeats(
        decode_smooth(rows, WINDOW_S, params["t_abs"], params["t_ratio"], params["k"]), 2.5
    )
    mixspec = power_spectrogram(decode(ROOT / mix["file"]))
    n = len(mixspec)
    songs = []
    for e in entries:
        al = alignment(rows, e.track, e.start, e.end)
        if al is None or e.track not in paths:
            continue
        ref = power_spectrogram(decode(paths[e.track]))
        # Each song may play from a little before its entry to a little after.
        span = (max(0.0, e.start - 10), min(n / FPS, e.end + 10))
        best = None
        for pitch in (False, True):
            w = warp(ref, *al, n, pitch)
            frames = slice(int(e.start * FPS), int(min(e.end, n / FPS) * FPS))
            # Fit quality where the entry says the song plays.
            m, s = mixspec[frames, MARGIN:-MARGIN], w[frames, MARGIN:-MARGIN]
            scale = np.sum(m * s) / max(np.sum(s * s), 1e-20)
            err = float(np.mean((np.log10(m + 1e-12) - np.log10(scale * s + 1e-12)) ** 2))
            if best is None or err < best[0]:
                best = (err, pitch, w)
        songs.append(
            {
                "track": e.track,
                "entry": e,
                "rate": al[1],
                "pitch": best[1],
                "spec": best[2],
                "span": span,
            }
        )
    if not songs:
        return []
    t = np.arange(n) / FPS

    def present(s: dict) -> np.ndarray:
        """Inside its span and where its warped reference has real power: a
        near-silent stretch (an excerpt's ends) would take any gain."""
        power = s["spec"].sum(axis=1)
        inside = (t >= s["span"][0]) & (t <= s["span"][1])
        typical = np.median(power[inside & (power > 0)]) if (inside & (power > 0)).any() else 0
        return inside & (power > 0.05 * typical)

    active = np.stack([present(s) for s in songs], axis=1)
    g = gains(mixspec, [s["spec"] for s in songs], active)
    g = uniform_filter1d(median_filter(g, size=(int(FPS * 0.5) | 1, 1)), size=int(FPS), axis=0)

    out = []
    truth = {seg["dense"]: seg for seg in mix["segments"] if seg["known"]}
    for k, s in enumerate(songs):
        alone = active[:, k] & (active.sum(axis=1) == 1)
        level = float(np.median(g[alone, k])) if alone.any() else float(np.max(g[:, k]))
        if level <= 0:
            continue
        seg = truth.get(s["track"])
        row = {"mix": i, "name": mix["name"], "track": s["track"], "rate": s["rate"],
               "turntable": s["pitch"], "known": seg is not None}  # fmt: skip
        e = s["entry"]
        x = g[:, k] / level
        # Two estimators of each fade, chosen between on the calibration sets.
        fin = crossings(g[:, k], level, rising=True)
        fout = crossings(g[:, k], level, rising=False)
        if fin:
            row["fade_in"] = fade_in_from(fin)
        if fout:
            row["fade_out"] = fade_out_from(fout)
        fin = fit_ramp(x, t, True, max(0.0, e.start - 5), e.start + 15)
        fout = fit_ramp(x, t, False, e.end - 15, min(t[-1], e.end + 5))
        if fin:
            row["ramp_fade_in"] = fin
        if fout:
            row["ramp_fade_out"] = fout
        row["baseline_fade_in"] = (e.start, e.start + WINDOW_S)
        row["baseline_fade_out"] = (e.end - WINDOW_S, e.end)
        if seg is not None:
            row["true_fade_in"] = (seg["mix_start"], seg["mix_start"] + seg["fade_in"])
            row["true_fade_out"] = (seg["mix_end"] - seg["fade_out"], seg["mix_end"])
            row["true_rate"] = seg["factor"]
            row["true_mode"] = seg["mode"]
            rise = (t - seg["mix_start"]) / max(seg["fade_in"], 1e-9)
            fall = (seg["mix_end"] - t) / max(seg["fade_out"], 1e-9)
            ramp = np.clip(np.minimum(rise, fall), 0, 1)
            fades = ((t >= seg["mix_start"]) & (t <= seg["mix_start"] + seg["fade_in"])) | (
                (t >= seg["mix_end"] - seg["fade_out"]) & (t <= seg["mix_end"])
            )
            if fades.any():
                row["gain_rmse_in_fades"] = float(
                    np.sqrt(np.mean((g[fades, k] / level - ramp[fades]) ** 2))
                )
        out.append(row)
    return out


def ref_paths(catalog: str) -> dict[int, Path]:
    cat = json.loads((ROOT / catalog / "catalog.json").read_text())
    extras = {
        e["fma_id"]: Path(e["path"])
        for e in json.loads((ROOT / catalog / "extras.json").read_text())
    }
    return {t["dense"]: extras[t["fma_id"]] for t in cat if t["fma_id"] in extras}


def label_fades(mix: dict) -> dict[int, dict]:
    """Dense id -> UnmixDB's own fade intervals for that song in this mix,
    read from the dataset's label file: unlike the converted ground truth,
    they include the first song's fade-in from silence and the last song's
    fade-out."""
    path = (ROOT / "data/unmixdb/extracted" / f"mixotic-{mix['set']}-excerpts" / "mixes"
            / f"{mix['name']}.labels.txt")  # fmt: skip
    by_file = {t["file"]: t for t in parse_labels(path.read_text()).values() if "file" in t}
    out = {}
    for seg in mix["segments"]:
        t = by_file.get(seg["fma_id"], {})
        out[seg["dense"]] = {k: t[k] for k in ("fadein", "fadeout") if k in t}
    return out


def rescore(out: Path) -> None:
    """Score a run's songs.json against every fade UnmixDB labels, including
    the first and last songs' (which the converted ground truth leaves out);
    writes label_summary.json. The method is unchanged: this only adds
    ground truth."""
    gt = json.loads((ROOT / "eval/mixes/unmixdb_s8.json").read_text())
    by_name = {m["name"]: m for m in gt["mixes"]}
    songs = json.loads((out / "songs.json").read_text())
    cache: dict[str, dict] = {}
    errs: dict[str, list[float]] = {}
    for r in songs:
        if not r.get("known"):
            continue
        lab = cache.setdefault(r["name"], label_fades(by_name[r["name"]])).get(r["track"], {})
        for kind, key in (("fade_in", "fadein"), ("fade_out", "fadeout")):
            truth = lab.get(key)
            if truth is None or truth[1] - truth[0] <= 0.5:
                continue
            conv = r.get("true_" + kind)
            edge = conv is None or conv[1] - conv[0] <= 0.5
            for est in ("ramp_" + kind, "baseline_" + kind):
                if est not in r:
                    continue
                for which, side in ((0, "start"), (1, "end")):
                    e = abs(r[est][which] - truth[which])
                    errs.setdefault(f"{est}_{side}", []).append(e)
                    if edge:
                        errs.setdefault(f"{est}_{side}_first_or_last_song", []).append(e)

    def stats(v: list[float]) -> dict:
        return {"n": len(v), "median": round(float(np.median(v)), 2),
                "p90": round(float(np.percentile(v, 90)), 2)}  # fmt: skip

    summary = {k: stats(v) for k, v in sorted(errs.items())}
    (out / "label_summary.json").write_text(json.dumps(summary, indent=1) + "\n")
    print(json.dumps(summary, indent=1))


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument(
        "--rescore", type=Path, help="score a run's songs.json against every labeled fade"
    )
    ap.add_argument(
        "--sets",
        nargs="+",
        help="develop on the calibration sets (set222 set275); test sets once",
    )
    ap.add_argument("--effects", nargs="+", help="only mixes with these effects (none, bass, ...)")
    ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--workers", type=int, default=6)
    ap.add_argument("--out", type=Path)
    args = ap.parse_args()
    if args.rescore:
        rescore(args.rescore)
        return
    if not args.sets or not args.out:
        ap.error("--sets and --out, or --rescore")
    gt = json.loads((ROOT / "eval/mixes/unmixdb_s8.json").read_text())
    params = json.loads(CALIBRATION.read_text())["smooth"]["params"]
    w = pd.read_csv(RES / "windows_unmixdb_s8_b2dhp.csv.gz")
    pairs = pair_rows(pd.read_csv(RES / "windows_unmixdb_s8_gridA2.csv.gz"))
    idx = [i for i, m in enumerate(gt["mixes"]) if m["set"] in args.sets
           and (not args.effects or m["effect"] in args.effects)][: args.limit or None]  # fmt: skip
    rows = combine(w[w["mix"].isin(idx)], pairs[pairs["mix"].isin(idx)], params["alpha"])
    paths = ref_paths("data/segments/medium_unmixdb_b2")
    jobs = [(i, gt["mixes"][i], rows[rows["mix"] == i], params, paths) for i in idx]
    with Pool(args.workers) as pool:
        results = [r for rs in pool.map(analyze, jobs) for r in rs]
    args.out.mkdir(parents=True, exist_ok=True)
    (args.out / "songs.json").write_text(json.dumps(results, indent=1, default=float) + "\n")
    summarize(results, args.out)


def summarize(results: list[dict], out: Path) -> None:
    def err(key: str, which: int, truth: str, edge: str) -> list[float]:
        return [abs(r[key][which] - r[truth][which]) for r in results
                if r.get("known") and key in r and truth in r and r[truth][1] - r[truth][0] > 0.5
                and edge in r]  # fmt: skip

    s = {"songs": len(results), "known": sum(r.get("known", False) for r in results)}
    for kind in ("fade_in", "fade_out"):
        for which, name in ((0, "start"), (1, "end")):
            for est in (kind, "ramp_" + kind, "baseline_" + kind):
                e = err(est, which, "true_" + kind, kind)
                if e:
                    med, p90 = float(np.median(e)), float(np.percentile(e, 90))
                    s[f"{est}_{name}_err_s"] = {"n": len(e), "median": round(med, 2),
                                                "p90": round(p90, 2)}  # fmt: skip
    rm = [r["gain_rmse_in_fades"] for r in results if "gain_rmse_in_fades" in r]
    if rm:
        s["gain_rmse_in_fades"] = {"n": len(rm), "median": round(float(np.median(rm)), 3)}
    tt = [
        r
        for r in results
        if r.get("known")
        and r.get("true_mode") in ("speed", "keylock")
        and abs(r["true_rate"] - 1) > 0.005
    ]
    if tt:
        s["turntable_vs_keylock_correct"] = round(
            float(np.mean([r["turntable"] == (r["true_mode"] == "speed") for r in tt])), 3
        )
    (out / "summary.json").write_text(json.dumps(s, indent=1) + "\n")
    print(json.dumps(s, indent=1))


if __name__ == "__main__":
    main()
