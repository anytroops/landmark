"""A phone recording of a DJ set in a venue, as close as a simulation gets
without measured rooms (docs/design/reverb.md).

  python -m eval.phone_sim --input set.wav --venue small_club --out data/queries/phone/x.m4a
  python -m eval.phone_sim --unmixdb --venue small_club

The chain, at 44.1 kHz: PA speakers (45 Hz to 16 kHz, a soft limiter); the
room, a shoebox impulse response with the direct sound and early reflections
from the image-source method and a late tail that decays faster at high
frequencies, its level from the venue's size and reverberation time (Sabine,
critical-distance formula); crowd noise (a DEMAND cafeteria recording, D52) at
the venue's SNR; the phone (150 Hz high-pass, automatic gain, AAC at 128
kbps). Not modeled: a real room's irregular shape, several speaker stacks, or
a particular phone's processing.

--unmixdb renders the UnmixDB test mixes, runs bin/tracklist -local on each,
scores them as the DJ evals do, and writes eval/results/local/phone_<venue>.json.
"""

import argparse
import concurrent.futures
import json
import subprocess
import tempfile
from dataclasses import dataclass
from pathlib import Path

import numpy as np
import soundfile as sf
from scipy.signal import butter, oaconvolve, resample_poly, sosfilt

from bench.scripts.benchcluster import BIN
from bench.scripts.live import B2_INDEX, PAIR_INDEX, git_head
from eval import demand
from eval.room_mixes import TEST_SETS, score

ROOT = Path(__file__).resolve().parent.parent
RES = ROOT / "eval" / "results" / "local"
RATE = 44_100
SOUND_MPS = 343.0
SPEAKER_Q = 4.0  # directivity of a PA stack
LATE_START_S = 0.08
REFLECTION_ORDER = 3
# Late tail per band: (low edge Hz, high edge Hz, reverberation time factor).
TAIL_BANDS = ((0.0, 500.0, 1.2), (500.0, 4000.0, 1.0), (4000.0, 0.0, 0.5))


@dataclass(frozen=True)
class Venue:
    size: tuple[float, float, float]  # meters
    rt60: float  # seconds, mid frequencies
    distance: float  # meters, speaker to phone
    snr_db: float  # music over crowd at the phone


VENUES = {
    "patio": Venue((15.0, 12.0, 4.0), 0.3, 5.0, 10.0),
    "small_club": Venue((12.0, 9.0, 3.5), 0.6, 5.0, 10.0),
    "mid_club": Venue((20.0, 15.0, 6.0), 0.9, 8.0, 10.0),
    "warehouse": Venue((40.0, 25.0, 8.0), 1.6, 15.0, 8.0),
}


def direct_to_reverb(v: Venue) -> float:
    """Direct over reverberant power at the phone: Q A / (16 pi r^2), with
    A = 0.161 V / RT60 (Sabine)."""
    volume = float(np.prod(v.size))
    return SPEAKER_Q * (0.161 * volume / v.rt60) / (16.0 * np.pi * v.distance**2)


def early_reflections(v: Venue, rate: int = RATE) -> np.ndarray:
    """Direct sound (amplitude 1) and image sources up to REFLECTION_ORDER in
    a shoebox whose walls absorb what its reverberation time implies."""
    lx, ly, lz = v.size
    area = 2.0 * (lx * ly + lx * lz + ly * lz)
    alpha = min(0.161 * lx * ly * lz / (area * v.rt60), 0.95)
    beta = np.sqrt(1.0 - alpha)
    src = np.array([1.0, ly / 2.0, 2.5])
    mic = np.array([min(1.0 + v.distance, lx - 0.5), min(ly / 2.0 + 1.0, ly - 0.5), 1.5])
    direct = float(np.linalg.norm(mic - src))
    arrivals: list[tuple[int, float]] = []
    span = range(-REFLECTION_ORDER, REFLECTION_ORDER + 1)
    for mx in span:
        for my in span:
            for mz in span:
                for px in (0, 1):
                    for py in (0, 1):
                        for pz in (0, 1):
                            order = (abs(mx - px) + abs(mx) + abs(my - py) + abs(my)
                                     + abs(mz - pz) + abs(mz))  # fmt: skip
                            if order > REFLECTION_ORDER:
                                continue
                            image = np.array(
                                [
                                    (1 - 2 * px) * src[0] + 2 * mx * lx,
                                    (1 - 2 * py) * src[1] + 2 * my * ly,
                                    (1 - 2 * pz) * src[2] + 2 * mz * lz,
                                ]
                            )
                            d = float(np.linalg.norm(image - mic))
                            delay = int(round((d - direct) / SOUND_MPS * rate))
                            arrivals.append((delay, beta**order * direct / d))
    h = np.zeros(max(a for a, _ in arrivals) + 1)
    for delay, gain in arrivals:
        h[delay] += gain
    return h


def late_tail(v: Venue, rng: np.random.Generator, rate: int = RATE) -> np.ndarray:
    """Noise decaying 60 dB per RT60 (faster at high frequencies), with the
    energy direct_to_reverb implies against a direct sound of 1."""
    n = int(round(1.5 * v.rt60 * 1.2 * rate))
    t = np.arange(n) / rate
    tail = np.zeros(n)
    for low, high, factor in TAIL_BANDS:
        noise = rng.standard_normal(n)
        if low == 0.0:
            sos = butter(4, high, "lowpass", fs=rate, output="sos")
        elif high == 0.0:
            sos = butter(4, low, "highpass", fs=rate, output="sos")
        else:
            sos = butter(4, [low, high], "bandpass", fs=rate, output="sos")
        tail += sosfilt(sos, noise) * 10.0 ** (-3.0 * t / (v.rt60 * factor))
    tail[: int(round(LATE_START_S * rate))] = 0.0
    return tail * np.sqrt(1.0 / direct_to_reverb(v) / np.sum(tail**2))


def impulse_response(v: Venue, rng: np.random.Generator, rate: int = RATE) -> np.ndarray:
    early, tail = early_reflections(v, rate), late_tail(v, rng, rate)
    h = np.zeros(max(len(early), len(tail)))
    h[: len(early)] += early
    h[: len(tail)] += tail
    return h


def speakers(x: np.ndarray, rate: int = RATE) -> np.ndarray:
    """The PA: 45 Hz to 16 kHz and a soft limiter, driven a little hot."""
    y = sosfilt(butter(2, [45.0, 16_000.0], "bandpass", fs=rate, output="sos"), x)
    level = max(float(np.quantile(np.abs(y), 0.99)), 1e-9)
    return np.tanh(1.5 * y / level)


def automatic_gain(x: np.ndarray, rate: int = RATE, threshold_db: float = -18.0,
                   ratio: float = 4.0) -> np.ndarray:  # fmt: skip
    """A phone's level control: compression of 20 ms blocks above threshold,
    5 ms attack and 200 ms release, peaks limited to full scale."""
    block = int(0.02 * rate)
    n = len(x) // block
    power = np.mean(x[: n * block].reshape(n, block) ** 2, axis=1) if n else np.empty(0)
    level_db = 10.0 * np.log10(power + 1e-12)
    wanted = np.minimum(0.0, (threshold_db - level_db) * (1.0 - 1.0 / ratio))
    attack, release = np.exp(-0.02 / 0.005), np.exp(-0.02 / 0.2)
    gain_db = np.zeros(n)
    current = 0.0
    for i, w in enumerate(wanted):
        coeff = attack if w < current else release
        current = coeff * current + (1.0 - coeff) * w
        gain_db[i] = current
    centers = (np.arange(n) + 0.5) * block
    gain = 10.0 ** (np.interp(np.arange(len(x)), centers, gain_db) / 20.0) if n else np.ones(len(x))
    y = x * gain
    return y / max(float(np.max(np.abs(y))), 1.0)


def crowd(n: int, rng: np.random.Generator, split: str = "test") -> np.ndarray:
    """n samples of the DEMAND cafeteria recording, tiled, at RATE."""
    source = demand.noise("PCAFETER", split).astype(np.float64)
    up = resample_poly(source, RATE, demand.SAMPLE_RATE)
    start = int(rng.integers(0, len(up)))
    return np.resize(np.roll(up, -start), n)


def phone_recording(x: np.ndarray, v: Venue, rng: np.random.Generator) -> np.ndarray:
    """What the phone's microphone and level control hand the encoder: mono
    x (at RATE) through the PA, the room, the crowd, and the phone."""
    wet = oaconvolve(speakers(x), impulse_response(v, rng))[: len(x)]
    noise = crowd(len(wet), rng)
    scale = np.sqrt(np.mean(wet**2) / (np.mean(noise**2) * 10.0 ** (v.snr_db / 10.0)))
    heard = wet + scale * noise
    heard = sosfilt(butter(2, 150.0, "highpass", fs=RATE, output="sos"), heard)
    return automatic_gain(heard / max(float(np.max(np.abs(heard))), 1e-9) * 0.7)


def read_mono(path: Path) -> np.ndarray:
    x, rate = sf.read(path, always_2d=True, dtype="float64")
    x = x.mean(axis=1)
    return resample_poly(x, RATE, rate) if rate != RATE else x


def encode(x: np.ndarray, out: Path) -> None:
    """AAC at 128 kbps, as a phone's voice-memo or video audio."""
    out.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory() as tmp:
        wav = Path(tmp) / "in.wav"
        sf.write(wav, x, RATE, subtype="PCM_16")
        cmd = ["ffmpeg", "-y", "-v", "error", "-i", str(wav), "-c:a", "aac", "-b:a", "128k"]
        subprocess.run([*cmd, str(out)], check=True)


def render(path: Path, venue: str, out: Path, seed: int) -> None:
    rng = np.random.default_rng([seed, sorted(VENUES).index(venue)])
    encode(phone_recording(read_mono(path), VENUES[venue], rng), out)


def unmixdb(venue: str, seed: int, parallel: int) -> dict:
    gt = json.loads((ROOT / "eval/mixes/unmixdb_s8.json").read_text())
    out_dir = ROOT / "data/queries/phone" / venue
    mixes = [(i, m) for i, m in enumerate(gt["mixes"]) if m["set"] in TEST_SETS]

    def one(job: tuple[int, dict]) -> dict:
        i, m = job
        path = out_dir / (Path(m["file"]).stem + ".m4a")
        if not path.exists():
            render(ROOT / m["file"], venue, path, seed + i)
        result = subprocess.run(
            [str(BIN / "tracklist"), "-local", "-index", str(B2_INDEX),
             "-pairs-index", str(PAIR_INDEX), "-timeout", "10s", "-json", str(path)],
            capture_output=True, text=True, check=True,
        ).stdout  # fmt: skip
        return {"file": m["file"], "entries": json.loads(result)}

    with concurrent.futures.ThreadPoolExecutor(parallel) as ex:
        runs = list(ex.map(one, mixes))
    RES.mkdir(parents=True, exist_ok=True)
    (RES / f"phone_{venue}.jsonl").write_text("".join(json.dumps(r) + "\n" for r in runs))
    result = {"venue": venue, "seed": seed, **score(runs, gt), **git_head()}
    (RES / f"phone_{venue}.json").write_text(json.dumps(result, indent=1) + "\n")
    return result


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--venue", choices=sorted(VENUES), required=True)
    ap.add_argument("--input", type=Path, help="render one file")
    ap.add_argument("--out", type=Path, help="with --input: the .m4a to write")
    ap.add_argument(
        "--unmixdb", action="store_true", help="render, tracklist, and score the test mixes"
    )
    ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--parallel", type=int, default=2)
    args = ap.parse_args()
    if args.unmixdb:
        print(json.dumps(unmixdb(args.venue, args.seed, args.parallel)))
    elif args.input and args.out:
        render(args.input, args.venue, args.out, args.seed)
    else:
        ap.error("--unmixdb, or --input and --out")


if __name__ == "__main__":
    main()
