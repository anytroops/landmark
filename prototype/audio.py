"""Audio decoding through an ffmpeg pipe. Nothing is cached to disk."""

import subprocess
from pathlib import Path

import numpy as np

SAMPLE_RATE = 11025
ENCODE_RATE = 44100


class DecodeError(Exception):
    """ffmpeg could not decode the file (corrupt, truncated, or empty)."""


def decode(path: str | Path, sample_rate: int = SAMPLE_RATE) -> np.ndarray:
    """Decode any ffmpeg-readable file to mono float32 at `sample_rate`."""
    cmd = [
        "ffmpeg", "-nostdin", "-v", "error",
        "-i", str(path),
        "-ac", "1", "-ar", str(sample_rate), "-f", "f32le", "-",
    ]  # fmt: skip
    proc = subprocess.run(cmd, capture_output=True, check=False)
    if proc.returncode != 0:
        raise DecodeError(f"{path}: {proc.stderr.decode(errors='replace').strip()}")
    pcm = np.frombuffer(proc.stdout, dtype=np.float32)
    if pcm.size == 0:
        raise DecodeError(f"{path}: decoded to zero samples")
    return pcm


def encode_decode_mp3(pcm: np.ndarray, bitrate: str, sample_rate: int = SAMPLE_RATE) -> np.ndarray:
    """Round-trip PCM through an MP3 encoder at `bitrate` (e.g. "64k").

    Encodes at 44.1 kHz: at 11,025 Hz MP3 (MPEG-2.5) caps at 64 kbps, so
    "128k" would silently equal "64k". Decodes back to `sample_rate`.
    """
    enc = subprocess.run(
        [
            "ffmpeg", "-nostdin", "-v", "error",
            "-f", "f32le", "-ar", str(sample_rate), "-ac", "1", "-i", "-",
            "-ar", str(ENCODE_RATE), "-b:a", bitrate, "-f", "mp3", "-",
        ],
        input=pcm.astype(np.float32).tobytes(),
        capture_output=True,
        check=True,
    )  # fmt: skip
    dec = subprocess.run(
        [
            "ffmpeg", "-nostdin", "-v", "error", "-f", "mp3", "-i", "-",
            "-ac", "1", "-ar", str(sample_rate), "-f", "f32le", "-",
        ],
        input=enc.stdout,
        capture_output=True,
        check=True,
    )  # fmt: skip
    return np.frombuffer(dec.stdout, dtype=np.float32)
