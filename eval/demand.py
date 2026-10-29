"""Real background noise for query degradations: DEMAND (Thiemann, Ito, and
Vincent, 2013; zenodo.org/records/1227121, CC BY 4.0), recordings of 18
everyday environments made with a 16-microphone array.

We use channel 1 of the 16 kHz release for ten environments where people
identify music (eating out, stations, streets, vehicles, home, a park),
resampled to the fingerprint's 11,025 Hz. Each recording is 5 minutes; val
queries draw their noise from the first half and test queries from the
second, so a noise window used for tuning never reaches test.

Prepare once (reads the downloaded zips, writes data/noise/demand/):
    python -m eval.demand prepare --zips data/noise_download
"""

import argparse
import functools
import hashlib
import io
import json
import zipfile
from pathlib import Path

import numpy as np
from scipy.io import wavfile
from scipy.signal import resample_poly

from prototype.audio import SAMPLE_RATE
from prototype.fma import DATA_DIR

NOISE_DIR = DATA_DIR / "noise" / "demand"
SOURCE_RATE = 16_000
# Condition names use these in lower case: demand_pcafeter_snr0_10s.
ENVIRONMENTS = (
    "PCAFETER",  # cafeteria: crowd chatter, dishes
    "PRESTO",  # restaurant
    "PSTATION",  # train station concourse
    "STRAFFIC",  # busy street
    "SPSQUARE",  # public square
    "TBUS",  # inside a bus
    "TCAR",  # inside a car
    "TMETRO",  # inside a subway train
    "DLIVING",  # living room
    "NPARK",  # city park
)


def prepare(zips: Path, out: Path) -> dict[str, dict[str, object]]:
    """Writes {ENV}.npy (float32 at SAMPLE_RATE) and manifest.json from the
    zips' channel 1, read from the archive in memory (nothing extracted)."""
    out.mkdir(parents=True, exist_ok=True)
    manifest: dict[str, dict[str, object]] = {}
    for env in ENVIRONMENTS:
        path = zips / f"{env}_16k.zip"
        with zipfile.ZipFile(path) as z:
            members = [n for n in z.namelist() if n.endswith("ch01.wav")]
            if len(members) != 1:
                raise ValueError(f"{path}: expected one ch01.wav, found {members}")
            rate, x = wavfile.read(io.BytesIO(z.read(members[0])))
        if rate != SOURCE_RATE or x.ndim != 1:
            raise ValueError(f"{path}: {rate} Hz, shape {x.shape}; want mono {SOURCE_RATE} Hz")
        if np.issubdtype(x.dtype, np.integer):
            x = x / float(np.iinfo(x.dtype).max + 1)
        y = resample_poly(x.astype(np.float64), SAMPLE_RATE, SOURCE_RATE).astype(np.float32)
        np.save(out / f"{env}.npy", y)
        manifest[env] = {
            "zip": path.name,
            "zip_md5": hashlib.md5(path.read_bytes()).hexdigest(),
            "member": members[0],
            "seconds": round(len(y) / SAMPLE_RATE, 2),
            "rms": float(np.sqrt(np.mean(y.astype(np.float64) ** 2))),
        }
    (out / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    return manifest


@functools.cache
def recording(env: str) -> np.ndarray:
    """One environment's prepared recording (cached per process)."""
    if env not in ENVIRONMENTS:
        raise KeyError(f"unknown DEMAND environment {env!r}")
    path = NOISE_DIR / f"{env}.npy"
    if not path.exists():
        raise FileNotFoundError(f"{path} missing: run python -m eval.demand prepare")
    return np.load(path)


def noise(env: str, split: str) -> np.ndarray:
    """The half of an environment's recording that a split's queries draw
    from: the first for val, the second for test."""
    x = recording(env)
    mid = len(x) // 2
    if split == "val":
        return x[:mid]
    if split == "test":
        return x[mid:]
    raise ValueError(f"unknown split {split!r}")


def main() -> None:
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("prepare", help="channel 1 of each zip, resampled, to data/noise/demand")
    p.add_argument("--zips", type=Path, required=True)
    p.add_argument("--out", type=Path, default=NOISE_DIR)
    args = ap.parse_args()
    for env, m in prepare(args.zips, args.out).items():
        print(env, m["seconds"], "s, rms", round(float(m["rms"]), 4))


if __name__ == "__main__":
    main()
