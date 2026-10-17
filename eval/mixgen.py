"""Synthetic DJ mixes with exact ground truth (D31/D32 evaluation).

  python -m eval.mixgen --split test --name test_mixed8 --mixes 20 --tracks 10 \\
      --mode mixed --max-pct 8 --seed 0

Each mix chains segments of split tracks with equal-power crossfades. Every
segment gets a tempo change: turntable style (tempo and pitch together) or
keylock (tempo only, by WSOLA; "stretch" uses librosa's phase vocoder
instead, a poor model of key lock, see eval.degrade), 0 with probability
--p-zero, else uniform in
+/- --max-pct. A fraction --unknown-p of segments come from the split's
never-indexed tracks, so a tracklister must also abstain. Tracks are drawn
without replacement across the whole set.

Optional DJ-booth degradations (all recorded in the ground truth): --eq-db
gives each segment random low-shelf (200 Hz) and high-shelf (3 kHz) gains
within +/- that many dB; --bass-swap plays each incoming segment high-passed
at 250 Hz through its fade-in, the usual way DJs keep two bass lines from
clashing; --snr adds pink noise (a stand-in for room and crowd noise; no
recorded crowd is used) at a per-mix level drawn from the range; --mp3
round-trips the finished mix through MP3, as mixes are usually shared.

Audio goes to data/mixes/<name>/ (never committed); the ground truth goes to
eval/mixes/<name>.json. In a segment, mix time t maps to reference time
src_start + (t - mix_start) * factor.
"""

import argparse
import json
from pathlib import Path

import numpy as np
import soundfile as sf
from scipy.signal import butter, lfilter, sosfilt

from eval.degrade import keylock, speed, stretch
from prototype.audio import SAMPLE_RATE, decode, encode_decode_mp3
from prototype.fma import DATA_DIR, audio_path

ROOT = Path(__file__).resolve().parent.parent


def fade_in(n: int) -> np.ndarray:
    return np.sin(np.linspace(0, np.pi / 2, n, dtype=np.float32))


def shelf(x: np.ndarray, gain_db: float, freq: float, high: bool, sr: int) -> np.ndarray:
    """RBJ-cookbook shelving filter (slope 1)."""
    a = 10 ** (gain_db / 40)
    w = 2 * np.pi * freq / sr
    cw, alpha = np.cos(w), np.sin(w) / 2 * np.sqrt(2)
    sa = 2 * np.sqrt(a) * alpha
    sign = -1 if high else 1
    b = [
        a * ((a + 1) - sign * (a - 1) * cw + sa),
        2 * sign * a * ((a - 1) - sign * (a + 1) * cw),
        a * ((a + 1) - sign * (a - 1) * cw - sa),
    ]
    den = [(a + 1) + sign * (a - 1) * cw + sa, -2 * sign * ((a - 1) + sign * (a + 1) * cw),
           (a + 1) + sign * (a - 1) * cw - sa]  # fmt: skip
    return lfilter(np.array(b) / den[0], np.array(den) / den[0], x).astype(np.float32)


def pink_noise(n: int, rng: np.random.Generator) -> np.ndarray:
    """Noise with power falling as 1/f, by shaping white noise's spectrum."""
    spec = np.fft.rfft(rng.standard_normal(n))
    f = np.arange(len(spec), dtype=np.float64)
    f[0] = 1
    return np.fft.irfft(spec / np.sqrt(f), n).astype(np.float32)


def make_set(args: argparse.Namespace) -> dict:
    manifest = json.loads(Path(args.manifest).read_text())
    subset = manifest["meta"]["subset"]
    dense = {f: i for i, f in enumerate(sorted(manifest["indexed"]))}
    rng = np.random.default_rng(args.seed)
    pos = list(rng.permutation(manifest[f"{args.split}_pos"]))
    neg = list(rng.permutation(manifest[f"{args.split}_neg"]))
    out_dir = DATA_DIR / "mixes" / args.name
    out_dir.mkdir(parents=True, exist_ok=True)
    sr = SAMPLE_RATE
    mixes = []
    for k in range(args.mixes):
        audio = np.zeros(0, dtype=np.float32)
        segments = []
        cursor = 0  # sample where the next segment starts
        overlap = 0  # samples the next segment overlaps the previous one (its fade-in)
        while len(segments) < args.tracks:
            known = rng.random() >= args.unknown_p
            pool = pos if known else neg
            if not pool:
                raise SystemExit(f"ran out of {'indexed' if known else 'unknown'} tracks")
            fma_id = int(pool.pop())
            try:
                pcm = decode(audio_path(fma_id, subset))
            except Exception as e:  # noqa: BLE001 (any decode failure: skip the track)
                print(f"skip {fma_id}: {e}")
                continue
            mode = args.mode if args.mode != "mixed" else str(rng.choice(["speed", "keylock"]))
            pct = 0.0 if rng.random() < args.p_zero else float(rng.uniform(-1, 1) * args.max_pct)
            pct = round(pct, 1)
            factor = 1 + pct / 100
            src_len = min(float(rng.uniform(*args.segment)), len(pcm) / sr - 0.5)
            src_start = float(rng.uniform(0, len(pcm) / sr - src_len))
            a = int(src_start * sr)
            seg = pcm[a : a + int(src_len * sr)].astype(np.float32)
            seg = {"speed": speed, "keylock": keylock, "stretch": stretch}[mode](seg, factor)
            low_db = high_db = 0.0
            if args.eq_db > 0:
                low_db, high_db = (float(v) for v in rng.uniform(-args.eq_db, args.eq_db, 2))
                seg = shelf(shelf(seg, low_db, 200, False, sr), high_db, 3000, True, sr)
            seg *= 0.1 / max(float(np.sqrt(np.mean(seg**2))), 1e-6)  # equal loudness
            # One fade length per transition: the outgoing fade-out and the
            # incoming fade-in cover the same overlap.
            first, last = len(segments) == 0, len(segments) == args.tracks - 1
            fade_out = 0 if last else int(float(rng.uniform(*args.crossfade)) * sr)
            fade_out = min(fade_out, len(seg) // 2 - overlap)
            if not first and args.bass_swap:
                hp = sosfilt(butter(2, 250, "highpass", fs=sr, output="sos"), seg).astype(
                    np.float32
                )
                blend = np.zeros(len(seg), dtype=np.float32)  # 1 = high-passed
                ramp = min(int(0.05 * sr), overlap)
                blend[: overlap - ramp] = 1
                blend[overlap - ramp : overlap] = np.linspace(1, 0, ramp, dtype=np.float32)
                seg = blend * hp + (1 - blend) * seg
            if not first:
                seg[:overlap] *= fade_in(overlap)
            if not last:
                seg[-fade_out:] *= fade_in(fade_out)[::-1]
            end = cursor + len(seg)
            if len(audio) < end:
                audio = np.concatenate([audio, np.zeros(end - len(audio), dtype=np.float32)])
            audio[cursor:end] += seg
            segments.append({
                "fma_id": fma_id, "dense": dense.get(fma_id, -1), "known": known,
                "mode": mode, "pct": pct, "factor": factor,
                "src_start": round(src_start, 3), "src_len": round(src_len, 3),
                "mix_start": round(cursor / sr, 3), "mix_end": round(end / sr, 3),
                "fade_in": round(overlap / sr, 3), "fade_out": round(fade_out / sr, 3),
                "eq_low_db": round(low_db, 2), "eq_high_db": round(high_db, 2),
            })  # fmt: skip
            cursor, overlap = end - fade_out, fade_out
        snr = None
        if args.snr:
            snr = float(rng.uniform(*args.snr))
            noise = pink_noise(len(audio), rng)
            p_sig, p_noise = float(np.mean(audio**2)), float(np.mean(noise**2))
            audio = audio + noise * np.sqrt(p_sig / (p_noise * 10 ** (snr / 10)))
        if args.mp3:
            audio = encode_decode_mp3(audio, args.mp3)
        audio *= 0.9 / max(float(np.abs(audio).max()), 1e-6)
        path = out_dir / f"mix_{k:03d}.wav"
        sf.write(path, audio, sr, subtype="PCM_16")
        mixes.append({"file": str(path.relative_to(ROOT)), "seconds": round(len(audio) / sr, 3),
                      "snr_db": None if snr is None else round(snr, 2),
                      "segments": segments})  # fmt: skip
        print(f"{path}: {len(audio) / sr:.0f} s, {len(segments)} segments")
    return {"name": args.name, "manifest": str(args.manifest), "split": args.split,
            "seed": args.seed, "mode": args.mode, "max_pct": args.max_pct,
            "p_zero": args.p_zero, "unknown_p": args.unknown_p,
            "segment_s": args.segment, "crossfade_s": args.crossfade,
            "eq_db": args.eq_db, "bass_swap": args.bass_swap, "snr_db": args.snr, "mp3": args.mp3,
            "sample_rate": sr, "mixes": mixes}  # fmt: skip


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--manifest", default=str(ROOT / "eval/manifests/splits_medium_seed0.json"))
    ap.add_argument("--split", choices=["val", "test"], required=True)
    ap.add_argument("--name", required=True)
    ap.add_argument("--mixes", type=int, default=10)
    ap.add_argument("--tracks", type=int, default=10, help="segments per mix")
    ap.add_argument("--mode", choices=["speed", "keylock", "stretch", "mixed"], default="mixed")
    ap.add_argument("--max-pct", type=float, default=8.0)
    ap.add_argument("--p-zero", type=float, default=0.2, help="share of segments left at 0%%")
    ap.add_argument("--unknown-p", type=float, default=0.15, help="share of never-indexed tracks")
    ap.add_argument("--segment", type=float, nargs=2, default=(18.0, 26.0), help="seconds, src")
    ap.add_argument("--crossfade", type=float, nargs=2, default=(3.0, 8.0), help="seconds")
    ap.add_argument("--eq-db", type=float, default=0.0, help="random shelf EQ range, dB")
    ap.add_argument("--bass-swap", action="store_true", help="high-pass incoming fade-ins")
    ap.add_argument("--snr", type=float, nargs=2, help="pink-noise SNR range, dB")
    ap.add_argument("--mp3", default="", help="round-trip the mix through MP3, e.g. 128k")
    ap.add_argument("--seed", type=int, default=0)
    args = ap.parse_args()
    gt = make_set(args)
    out = ROOT / "eval" / "mixes" / f"{args.name}.json"
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(gt, indent=1) + "\n")
    print(out)


if __name__ == "__main__":
    main()
