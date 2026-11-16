"""Rendered DJ mixes with exact mixer moves, for the mixer-moves model
(docs/design/mixer-moves.md).

  python -m eval.mixsim --split train --mixes 1200 --seed 0
  python -m eval.mixsim --split val --mixes 150 --seed 1
  python -m eval.mixsim --split test --mixes 150 --seed 2

Each mix chains SONGS fma_medium clips (the split's tracks: train draws from
the indexed tracks outside the val and test splits). Every song plays at a
tempo change (turntable or key lock, as eval.mixgen), then through an
isolator EQ (zero-phase crossovers at 250 Hz and 2.5 kHz; the bands sum
back to the input exactly) and its channel fader. Each transition draws one
move (crossfade, cut, bass swap, filter-style sweep, mid dip), and any song
may get a static trim. The label at every spectrogram frame is each song's
gain in each band, fader times EQ times the mix's master gain, at the
frame's center. After the mixer, the recording chain the labels do not see
(record): a master limiter, room noise, and MP3 on half the mixes.

Writes data/mixsim/<split>/<n>.npz (never committed): the mix's triplet
log spectrogram, and per song its track, tempo factor and mode, the mix
frames it plays, its alignment, and its gains.
"""

import argparse
import json
import multiprocessing as mp
from pathlib import Path

import numpy as np

from eval.degrade import keylock, speed
from eval.mixgen import pink_noise
from prototype import b2
from prototype.audio import SAMPLE_RATE, DecodeError, decode, encode_decode_mp3
from prototype.fma import audio_path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "data" / "mixsim"
SONGS = 6
CROSSOVERS_HZ = (250.0, 2500.0)
FLOOR = 10.0 ** (-40.0 / 20.0)  # gains below -40 dB count as off
MOVES = ("crossfade", "cut", "bass_swap", "sweep", "mid_dip")
MOVE_P = (0.35, 0.1, 0.3, 0.15, 0.1)
CONTROL_HZ = 100  # automation curves are drawn at this rate, then interpolated
MIN_CLIP_S = 12.0  # shorter clips (a few fma_medium tracks) are drawn again


def split_tracks(split: str) -> list[int]:
    m = json.loads((ROOT / "eval/manifests/splits_medium_seed0.json").read_text())
    if split == "train":
        held = set(m["val_pos"]) | set(m["test_pos"])
        return sorted(t for t in m["indexed"] if t not in held)
    return sorted(m[f"{split}_pos"] + m[f"{split}_neg"])


def band_masks(n: int, rate: int = SAMPLE_RATE) -> np.ndarray:
    """Zero-phase isolator masks for an n-point real FFT (3 x n // 2 + 1):
    each crossover a raised cosine over a third of an octave; the three sum
    to 1 at every frequency."""
    f = np.fft.rfftfreq(n, 1.0 / rate)
    logf = np.log2(np.maximum(f, 1e-3))

    def below(cut: float) -> np.ndarray:
        u = np.clip((logf - (np.log2(cut) - 1 / 6)) / (1 / 3), 0.0, 1.0)
        return 0.5 + 0.5 * np.cos(np.pi * u)

    low, not_high = below(CROSSOVERS_HZ[0]), below(CROSSOVERS_HZ[1])
    return np.stack([low, not_high - low, 1.0 - not_high])


def bands(x: np.ndarray, rate: int = SAMPLE_RATE) -> np.ndarray:
    """x split into low, mid, high (3 x n) by band_masks; the three sum to x."""
    spectrum = np.fft.rfft(x)
    return np.fft.irfft(band_masks(len(x), rate) * spectrum[None], n=len(x))


def ramp(n: int, shape: str) -> np.ndarray:
    """A rising curve from 0 to 1 over n control points."""
    u = np.linspace(0.0, 1.0, max(n, 2))
    if shape == "linear":
        return u
    if shape == "equal_power":
        return np.sin(u * np.pi / 2)
    return 0.5 - 0.5 * np.cos(u * np.pi)  # s-curve


def db(x: float) -> float:
    return 10.0 ** (x / 20.0)


def automate(
    n: int, plays: list[tuple[int, int]], rng: np.random.Generator
) -> tuple[np.ndarray, list]:
    """Gains (songs x 3 bands x n control points), fader times EQ times trim,
    for songs playing over plays[i] = (first, end) control points; and each
    transition's move."""
    k = len(plays)
    fader = np.zeros((k, n))
    eq = np.ones((k, 3, n))
    trim = np.ones((k, 3))
    for i, (a, b) in enumerate(plays):
        fader[i, a:b] = 1.0
        if rng.random() < 0.3:
            trim[i] = 10.0 ** (rng.uniform(-6.0, 6.0, 3) / 20.0)
    moves = []
    for i in range(k - 1):
        a_in, end_out = plays[i + 1][0], plays[i][1]
        over = end_out - a_in
        move = str(rng.choice(MOVES, p=MOVE_P))
        info: dict = {"move": move, "start": a_in, "end": end_out}
        if move == "cut":
            at = a_in + int(rng.integers(over // 4, max(3 * over // 4, over // 4 + 1)))
            fader[i + 1, a_in:at] = 0.0
            fader[i, at:end_out] = 0.0
            info["at"] = at
        elif move == "bass_swap":
            up = int(rng.integers(CONTROL_HZ, max(over // 3, CONTROL_HZ + 1)))
            fader[i + 1, a_in : a_in + up] *= ramp(up, "equal_power")[:up]
            swap = a_in + int(rng.integers(over // 3, max(2 * over // 3, over // 3 + 1)))
            stop = min(swap + int(rng.integers(CONTROL_HZ // 4, 2 * CONTROL_HZ)), end_out)
            killed = 10.0 ** (-rng.uniform(25.0, 40.0) / 20.0)
            rise = killed + (1.0 - killed) * ramp(stop - swap, "linear")[: stop - swap]
            eq[i + 1, 0, a_in:swap] = killed
            eq[i + 1, 0, swap:stop] = rise
            eq[i, 0, swap:stop] = rise[::-1]
            eq[i, 0, stop:end_out] = killed
            leave = int(rng.integers(stop, max(end_out, stop + 1)))
            fader[i, leave:end_out] *= ramp(end_out - leave, "equal_power")[::-1][: end_out - leave]
            info["swap"] = swap
        else:  # crossfade, sweep, mid dip: the faders cross over part of the overlap
            lo = int(rng.integers(0, max(over // 3, 1)))
            hi_w = over - lo
            lo_w = min(max(over // 3, 2 * CONTROL_HZ), hi_w)
            width = int(rng.integers(lo_w, hi_w + 1))
            shape = str(rng.choice(["linear", "equal_power", "s_curve"]))
            up = ramp(width, shape)[:width]
            fader[i + 1, a_in : a_in + lo] = 0.0
            fader[i + 1, a_in + lo : a_in + lo + width] *= up
            fader[i, a_in + lo : a_in + lo + width] *= up[::-1]
            fader[i, a_in + lo + width : end_out] = 0.0
            info.update(fade=[a_in + lo, a_in + lo + width], shape=shape)
            if move == "sweep":
                band = int(rng.choice([0, 2]))  # a low cut or a high cut on the outgoing song
                eq[i, band, a_in:end_out] = 10.0 ** (
                    -np.linspace(0.0, rng.uniform(25, 40), over) / 20
                )
                info["band"] = band
            elif move == "mid_dip":
                eq[i, 1, a_in:end_out] = 10.0 ** (-rng.uniform(6.0, 15.0) / 20.0)
        moves.append(info)
    return fader[:, None, :] * eq * trim[:, :, None], moves


def record(mix: np.ndarray, rng: np.random.Generator) -> tuple[np.ndarray, dict]:
    """The recording chain after the mixer, which the labels do not see: a
    master limiter driven 0 to 6 dB into a soft clip, room noise at 30 to 45 dB
    below the music, and an MP3 round trip at 128 kbps on half the mixes."""
    drive_db = float(rng.uniform(0.0, 6.0))
    peak = max(float(np.quantile(np.abs(mix), 0.999)), 1e-9)
    drive = 10.0 ** (drive_db / 20.0)
    y = np.tanh(drive * mix / peak) * peak / drive
    snr_db = float(rng.uniform(30.0, 45.0))
    noise = pink_noise(len(y), rng)
    y = y + noise * np.sqrt(np.mean(y**2) / (np.mean(noise**2) * 10.0 ** (snr_db / 10.0)))
    mp3 = bool(rng.random() < 0.5)
    if mp3:
        y = encode_decode_mp3(y.astype(np.float32), "128k").astype(np.float64)[: len(mix)]
        y = np.pad(y, (0, len(mix) - len(y)))
    return y, {"limiter_db": round(drive_db, 2), "noise_snr_db": round(snr_db, 2), "mp3": mp3}


def render(tracks: list[int], rng: np.random.Generator) -> dict | None:
    """One mix of the given tracks; None if a clip does not decode or is
    shorter than MIN_CLIP_S. The
    gains include a master gain drawn per mix, so each is the song's level in
    the mix relative to its own file, which the audio determines."""
    clips, factors, modes = [], [], []
    for t in tracks:
        try:
            pcm = decode(audio_path(t, "medium"))
        except DecodeError:
            return None
        if len(pcm) < MIN_CLIP_S * SAMPLE_RATE:  # too short to overlap; the caller draws again
            return None
        pct = 0.0 if rng.random() < 0.2 else float(rng.uniform(-8, 8))
        mode = str(rng.choice(["speed", "keylock"]))
        f = 1.0 + pct / 100.0
        played = pcm if pct == 0.0 else (speed(pcm, f) if mode == "speed" else keylock(pcm, f))
        clips.append(played.astype(np.float64))
        factors.append(f)
        modes.append(mode)
    starts = [0]
    for i in range(1, len(clips)):
        shortest = min(len(clips[i - 1]), len(clips[i])) / SAMPLE_RATE
        over = float(rng.uniform(4.0, min(20.0, 0.6 * shortest)))
        starts.append(starts[-1] + len(clips[i - 1]) - int(over * SAMPLE_RATE))
    total = starts[-1] + len(clips[-1])
    n_ctrl = int(np.ceil(total / SAMPLE_RATE * CONTROL_HZ)) + 1
    to_ctrl = CONTROL_HZ / SAMPLE_RATE
    plays = [(int(np.ceil(s * to_ctrl)), int((s + len(c)) * to_ctrl))
             for s, c in zip(starts, clips, strict=True)]  # fmt: skip
    g, moves = automate(n_ctrl, plays, rng)
    g *= 10.0 ** (rng.uniform(-12.0, 6.0) / 20.0)  # the master
    ctrl_t = np.arange(n_ctrl) / CONTROL_HZ
    mix = np.zeros(total)
    for i, (s, c) in enumerate(zip(starts, clips, strict=True)):
        t = (s + np.arange(len(c))) / SAMPLE_RATE
        split = bands(c)
        for b in range(3):
            mix[s : s + len(c)] += split[b] * np.interp(t, ctrl_t, g[i, b])
    mix, chain = record(mix, rng)
    spec = b2.spectrogram(mix.astype(np.float32))
    hop, half = b2.DEFAULT.hop, b2.DEFAULT.n_fft // 2
    centers = (np.arange(len(spec)) * hop + half) / SAMPLE_RATE
    songs = []
    for i, (s, c) in enumerate(zip(starts, clips, strict=True)):
        first = max(int(np.ceil((s - half) / hop)), 0)
        last = min((s + len(c) - 1 - half) // hop, len(spec) - 1)
        gains = np.stack(
            [np.interp(centers[first : last + 1], ctrl_t, g[i, b]) for b in range(3)], axis=1
        )
        songs.append({"track": tracks[i], "factor": factors[i], "mode": modes[i], "first": first,
                      "last": int(last), "a": -s / SAMPLE_RATE * factors[i], "rate": factors[i],
                      "gains": gains.astype(np.float32)})  # fmt: skip
    return {"spec": spec.astype(np.float16), "songs": songs, "moves": moves, "chain": chain}


def _one(job: tuple[str, int, int, list[int]]) -> int:
    split, seed, k, pool = job
    rng = np.random.default_rng([seed, k])
    out = OUT / split / f"{k:05d}.npz"
    if out.exists():
        return k
    for _ in range(5):
        mix = render([int(t) for t in rng.choice(pool, SONGS, replace=False)], rng)
        if mix is not None:
            break
    else:
        return -1
    s = mix["songs"]
    np.savez_compressed(
        out, spec=mix["spec"],
        tracks=np.array([x["track"] for x in s]), factors=np.array([x["factor"] for x in s]),
        modes=np.array([x["mode"] for x in s]), first=np.array([x["first"] for x in s]),
        last=np.array([x["last"] for x in s]), a=np.array([x["a"] for x in s]),
        rate=np.array([x["rate"] for x in s]), moves=json.dumps(mix["moves"]),
        chain=json.dumps(mix["chain"]),
        **{f"gains{i}": x["gains"] for i, x in enumerate(s)},
    )  # fmt: skip
    return k


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--split", choices=["train", "val", "test"], required=True)
    ap.add_argument("--mixes", type=int, required=True)
    ap.add_argument("--seed", type=int, required=True)
    ap.add_argument("--workers", type=int, default=10)
    args = ap.parse_args()
    (OUT / args.split).mkdir(parents=True, exist_ok=True)
    pool = split_tracks(args.split)
    jobs = [(args.split, args.seed, k, pool) for k in range(args.mixes)]
    with mp.get_context("fork").Pool(args.workers) as p:
        done = [k for k in p.imap_unordered(_one, jobs, chunksize=4) if k >= 0]
    print(f"{len(done)} mixes in {OUT / args.split}")


if __name__ == "__main__":
    main()
