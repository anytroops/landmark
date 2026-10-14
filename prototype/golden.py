"""Export golden landmark sets for Go parity tests (Phase 2).

For each selected track: FMA id, params, and the (hash, t1) landmarks the
Python fingerprinter produces from the full clip. The Go test decodes the
same local MP3 and compares landmark sets by Jaccard similarity. Audio is
not exported; the Go test skips when data/ is absent.
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


def main() -> None:
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
