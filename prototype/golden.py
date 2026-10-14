"""Export golden landmark sets for Go parity tests (Phase 2).

For each selected track: FMA id, params, and the (hash, t1) landmarks the
Python fingerprinter produces from the full clip. The Go test decodes the
same local MP3 and compares landmark sets by Jaccard similarity. Audio is
not exported; the Go test skips when data/ is absent.

synthetic.json uses a signal both languages generate from a formula
(chirp + gated tone + tone + integer-LCG noise), so parity runs in CI with
no audio. Keep synthetic_signal in sync with internal/fingerprint's copy.
"""

import json
from dataclasses import asdict
from pathlib import Path

import numpy as np

from prototype import fingerprint as fp
from prototype.audio import decode
from prototype.fma import audio_path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "testdata" / "golden"
N_TRACKS = 20
SYNTH_SECONDS = 10


def synthetic_signal(n: int, sr: int = 11025) -> np.ndarray:
    """Deterministic test signal, computed in float64 then rounded to float32."""
    tau = np.arange(n, dtype=np.float64) / sr
    chirp = 0.25 * np.sin(2 * np.pi * (150 * tau + 0.5 * 250 * tau * tau))
    gate = (np.floor(tau * 4).astype(np.int64) % 3 == 0).astype(np.float64)
    tones = 0.15 * gate * np.sin(2 * np.pi * 440 * tau) + 0.1 * np.sin(2 * np.pi * 1234.5 * tau)
    state = 12345
    noise = np.empty(n, dtype=np.float64)
    for i in range(n):
        state = (state * 1664525 + 1013904223) & 0xFFFFFFFF
        noise[i] = 0.02 * (state / 2**32 - 0.5)
    return (chirp + tones + noise).astype(np.float32)


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    n = SYNTH_SECONDS * 11025
    h, t = fp.fingerprint(synthetic_signal(n))
    doc = {"signal": "synthetic_v1", "params": asdict(fp.DEFAULT), "n_samples": n,
           "hashes": h.tolist(), "times": t.tolist()}  # fmt: skip
    (OUT / "synthetic.json").write_text(json.dumps(doc, separators=(",", ":")) + "\n")
    print("synthetic", len(h))

    manifest = json.loads((ROOT / "eval/manifests/splits_small_seed0.json").read_text())
    ids = np.random.default_rng(0).choice(manifest["indexed"], N_TRACKS, replace=False)
    OUT.mkdir(parents=True, exist_ok=True)
    for fid in sorted(int(i) for i in ids):
        pcm = decode(audio_path(fid))
        h, t = fp.fingerprint(pcm)
        doc = {
            "fma_id": fid,
            "subset": "small",
            "params": asdict(fp.DEFAULT),
            "n_samples": len(pcm),
            "hashes": h.tolist(),
            "times": t.tolist(),
        }
        (OUT / f"{fid:06d}.json").write_text(json.dumps(doc, separators=(",", ":")) + "\n")
        print(fid, len(h))


if __name__ == "__main__":
    main()
