"""Search DJ-mix windows under speed hypotheses (D31 option A).

  # every window, every hypothesis on the grid; writes per-hypothesis rows
  python -m eval.tracklist search --mixes test_mixed8 --shard 127.0.0.1:7201 \\
      --stoplist data/segments/medium/p1/stoplist.lmst --stop-fraction 0.04

  # ground-truth check: one window per known segment, undone at its true speed
  python -m eval.tracklist oracle --mixes smoke --shard 127.0.0.1:7201

A hypothesis (mode, factor) undoes a tempo change, in one of two ways:
"audio" resamples (turntable) or WSOLA-stretches (keylock) the window by
1/factor and fingerprints it again; "landmarks" fingerprints the window
once and rescales its landmarks: times and hash time gaps by factor, and
for turntable the hash frequencies by 1/factor. --undo auto (default) uses
audio for turntable and landmarks for keylock, the better of the two for
each on the smoke-mix oracle check. The window plays the
reference `factor` times faster, so reference time = mix time * factor.
Factor 1 is the unchanged window. The grid spans +/- --max-pct
in --step-pct steps for each mode (D31: 1% steps keep turntable queries in
the zone where today's hashes still match, EVAL speed tolerance).

Offsets are reference frame minus query frame at 43.07 frames per second
(hop 256 at 11,025 Hz), so a window starting at reference time r seconds
matches at about r * 43.07, in the undone (reference) time base.
"""

import argparse
import json
import os
from multiprocessing import Pool
from pathlib import Path

import numpy as np
import pandas as pd
import soundfile as sf

from eval.backends import ShardBackend, dense_tables
from eval.degrade import keylock, speed
from prototype import fingerprint as fp
from prototype.stoplist import StopList

ROOT = Path(__file__).resolve().parent.parent
RES = ROOT / "eval" / "results" / "tracklist"
FRAMES_PER_S = 11025 / 256


def cluster_table(manifest: str, catalog: str | None) -> np.ndarray:
    """Dense id -> duplicate-cluster representative, from the searched index's
    catalog.json when given (any index), else from the split manifest (an
    index built from it, like fma_medium's)."""
    if catalog:
        cat = json.loads(Path(catalog).read_text())
        return np.array([t["cluster"] for t in cat], dtype=np.int64)
    return dense_tables(json.loads(Path(manifest).read_text()))[1]


def hypotheses(max_pct: float, step_pct: float, modes: list[str],
               extra: tuple[float, ...] = ()) -> list[tuple[str, float]]:  # fmt: skip
    """(mode, factor) pairs: factor 1 once, then each nonzero step for each
    mode, then each extra factor (e.g. 2 and 0.5 for double- and half-time
    tempo matching) for each mode."""
    steps = np.round(np.arange(step_pct, max_pct + 1e-9, step_pct), 4)
    out = [("none", 1.0)]
    for mode in modes:
        for s in steps:
            out += [(mode, 1 + s / 100), (mode, 1 - s / 100)]
        out += [(mode, float(f)) for f in extra]
    return out


def undo(x: np.ndarray, mode: str, factor: float) -> np.ndarray:
    if mode == "none":
        return x
    return (speed if mode == "speed" else keylock)(x, 1 / factor)


def rescale(
    h: np.ndarray, t: np.ndarray, mode: str, factor: float
) -> tuple[np.ndarray, np.ndarray]:
    """Undo a tempo change on landmarks: no second pass over the audio."""
    if mode == "none":
        return h, t
    f1, f2, dt = fp.unpack(h)
    if mode == "speed":  # turntable: pitch moved with tempo
        f1, f2 = np.round(f1 / factor).astype(np.int64), np.round(f2 / factor).astype(np.int64)
    dt = np.round(dt * factor).astype(np.int64)
    t2 = np.round(t.astype(np.float64) * factor).astype(np.int64)
    ok = (dt >= 1) & (dt <= fp.MAX_DT) & (f1 < fp.N_BINS) & (f2 < fp.N_BINS)
    h2 = (f1 << (fp.F_BITS + fp.DT_BITS)) | (f2 << fp.DT_BITS) | dt
    packed = np.unique((h2[ok].astype(np.uint64) << np.uint64(32)) | t2[ok].astype(np.uint64))
    return (packed >> np.uint64(32)).astype(np.uint32), (packed & np.uint64(0xFFFFFFFF)).astype(
        np.uint32
    )


# Per-process search state, set once by the pool initializer (a gRPC channel
# cannot cross a fork, so each worker opens its own).
_STATE: dict = {}


def _init(shard: str, cluster: np.ndarray, stoplist: str | None, fraction: float) -> None:
    _STATE["backend"] = ShardBackend(shard, cluster)
    _STATE["stop"] = StopList.load(Path(stoplist)) if stoplist and fraction > 0 else None
    _STATE["limit"] = _STATE["stop"].limit(fraction) if _STATE["stop"] is not None else None


def _search(job: tuple) -> list[dict]:
    mix, t0, x, hyps, shifts, how = job
    rows = []
    base = fp.fingerprint_query(x, shifts) if how != "audio" else None
    for mode, factor in hyps:
        on_landmarks = how == "landmarks" or (how == "auto" and mode != "speed")
        if on_landmarks:
            h, t = rescale(*base, mode, factor)
        else:
            h, t = fp.fingerprint_query(undo(x, mode, factor), shifts)
        if _STATE["stop"] is not None:
            keep = _STATE["stop"].keep(h, _STATE["limit"])
            h, t = h[keep], t[keep]
        hit = _STATE["backend"].search(h, t)
        rows.append({"mix": mix, "t0": t0, "mode": mode, "factor": factor,
                     "top1_dense": hit.top1_dense, "score": hit.top1_score,
                     "runner_up": hit.runner_up, "offset": hit.top1_offset,
                     "n_landmarks": len(h)})  # fmt: skip
    return rows


def load_set(name: str) -> dict:
    return json.loads((ROOT / "eval" / "mixes" / f"{name}.json").read_text())


def search_cmd(args: argparse.Namespace, cluster: np.ndarray) -> None:
    gt = load_set(args.mixes)
    sr = gt["sample_rate"]
    hyps = hypotheses(args.max_pct, args.step_pct, args.modes, tuple(args.extra_factors))
    jobs = []
    for k, m in enumerate(gt["mixes"]):
        audio, file_sr = sf.read(ROOT / m["file"], dtype="float32")
        assert file_sr == sr, f"{m['file']} is {file_sr} Hz, ground truth says {sr}"
        w, hop = int(args.window * sr), int(args.hop * sr)
        for a in range(0, len(audio) - w + 1, hop):
            jobs.append((k, round(a / sr, 3), audio[a : a + w], hyps, args.shifts, args.undo))
    print(f"{len(jobs)} windows x {len(hyps)} hypotheses")
    rows = []
    init = (args.shard, cluster, args.stoplist, args.stop_fraction)
    with Pool(args.workers, initializer=_init, initargs=init) as pool:
        for i, r in enumerate(pool.imap_unordered(_search, jobs, chunksize=2), 1):
            rows += r
            if i % 100 == 0:
                print(f"{i}/{len(jobs)} windows", flush=True)
    RES.mkdir(parents=True, exist_ok=True)
    out = RES / f"windows_{args.mixes}{args.tag}.csv.gz"
    pd.DataFrame(rows).sort_values(["mix", "t0", "mode", "factor"]).to_csv(out, index=False)
    meta = {"mixes": args.mixes, "window_s": args.window, "hop_s": args.hop,
            "max_pct": args.max_pct, "step_pct": args.step_pct, "modes": args.modes,
            "extra_factors": args.extra_factors,
            "hypotheses": len(hyps), "shifts": args.shifts, "undo": args.undo, "shard": args.shard,
            "stop_fraction": args.stop_fraction, "windows": len(jobs),
            "rows": len(rows)}  # fmt: skip
    out.with_name(f"windows_{args.mixes}{args.tag}.meta.json").write_text(
        json.dumps(meta, indent=1) + "\n"
    )
    print(out)


def oracle_cmd(args: argparse.Namespace, cluster: np.ndarray) -> None:
    """Undo each known segment's true tempo change on a window from the part
    where it plays alone; the right track at the predicted offset validates
    the ground truth's time mapping."""
    gt = load_set(args.mixes)
    sr = gt["sample_rate"]
    _init(args.shard, cluster, None, 0)
    methods = ("audio", "landmarks", "auto")
    ok = dict.fromkeys(methods, 0)
    total = 0
    for k, m in enumerate(gt["mixes"]):
        audio, _ = sf.read(ROOT / m["file"], dtype="float32")
        for s in m["segments"]:
            solo_a, solo_b = s["mix_start"] + s["fade_in"], s["mix_end"] - s["fade_out"]
            if not s["known"] or s.get("ignore") or solo_b - solo_a < args.window:
                continue
            t0 = (solo_a + solo_b - args.window) / 2
            x = audio[int(t0 * sr) : int(t0 * sr) + int(args.window * sr)]
            hyp = [(s["mode"] if s["pct"] else "none", s["factor"])]
            ref = s["src_start"] + (t0 - s["mix_start"]) * s["factor"]
            line = f"mix {k} t0 {t0:6.1f} {s['mode']:7s} {s['pct']:+5.1f}% want {s['dense']:6d}"
            total += 1
            for how in methods:
                row = _search((k, t0, x, hyp, 4, how))[0]
                err_s = row["offset"] / FRAMES_PER_S - ref
                good = row["top1_dense"] == s["dense"] and abs(err_s) < args.tolerance
                ok[how] += good
                line += f" | {how}: got {row['top1_dense']:6d} score {row['score']:5d} "
                line += f"err {err_s:+.3f} s{'' if good else ' X'}"
            print(line)
    for how in methods:
        print(f"{how}: {ok[how]}/{total} known segments identified at the predicted offset")
    if ok["auto"] != total:
        raise SystemExit(1)


def main() -> None:
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    for name in ("search", "oracle"):
        p = sub.add_parser(name)
        p.add_argument("--mixes", required=True, help="ground-truth set name in eval/mixes")
        p.add_argument("--shard", required=True)
        p.add_argument("--manifest", default=str(ROOT / "eval/manifests/splits_medium_seed0.json"))
        p.add_argument("--window", type=float, default=10.0)
        p.add_argument("--catalog", help="catalog.json of the searched index (default: manifest)")
        p.add_argument("--tolerance", type=float, default=0.25, help="oracle offset tolerance, s")
    s = sub.choices["search"]
    s.add_argument("--hop", type=float, default=2.5)
    s.add_argument("--max-pct", type=float, default=8.0)
    s.add_argument("--step-pct", type=float, default=1.0)
    s.add_argument("--modes", nargs="+", default=["speed", "keylock"])
    s.add_argument("--extra-factors", type=float, nargs="*", default=[],
                   help="extra tempo factors per mode, e.g. 2 0.5")  # fmt: skip
    s.add_argument("--shifts", type=int, default=4)
    s.add_argument("--undo", choices=["audio", "landmarks", "auto"], default="auto")
    s.add_argument("--stoplist")
    s.add_argument("--stop-fraction", type=float, default=0.0)
    s.add_argument("--workers", type=int, default=os.cpu_count())
    s.add_argument("--tag", default="")
    args = ap.parse_args()
    cluster = cluster_table(args.manifest, args.catalog)
    (search_cmd if args.cmd == "search" else oracle_cmd)(args, cluster)


if __name__ == "__main__":
    main()
