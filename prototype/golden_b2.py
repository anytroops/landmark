"""Export golden B2 triplet landmarks for Go parity tests (D31 option B2, D33).

  python -m prototype.golden_b2

For the synthetic signal (prototype.golden.synthetic_signal, which Go also
generates) and a few fma_medium tracks: params, and the (hash, t, span, band)
landmarks of the full clip; for the synthetic signal also the 4-shift query
landmarks of its first 10 s. The Go test compares sets by Jaccard similarity
(D17: only FFT rounding may differ). Audio is not exported; the Go test skips
FMA files when data/ is absent.
"""

import json
from dataclasses import asdict
from pathlib import Path

import numpy as np

from prototype import b2
from prototype.audio import decode
from prototype.fma import audio_path
from prototype.golden import synthetic_signal

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "testdata" / "golden_b2"
N_TRACKS = 8


def landmarks(lm: b2.Landmarks) -> dict[str, list[int]]:
    return {"hashes": lm.hash.tolist(), "times": lm.t.tolist(),
            "spans": lm.span.tolist(), "bands": lm.band.tolist()}  # fmt: skip


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    n = 30 * 11025
    x = synthetic_signal(n)
    doc = {"signal": "synthetic_v1", "params": asdict(b2.DEFAULT), "n_samples": n,
           **landmarks(b2.fingerprint(x)),
           "query": landmarks(b2.fingerprint_query(x[: 10 * 11025], 4))}  # fmt: skip
    (OUT / "synthetic.json").write_text(json.dumps(doc, separators=(",", ":")) + "\n")
    print("synthetic", len(doc["hashes"]), "query", len(doc["query"]["hashes"]))

    manifest = json.loads((ROOT / "eval/manifests/splits_medium_seed0.json").read_text())
    ids = np.random.default_rng(0).choice(manifest["indexed"], N_TRACKS, replace=False)
    for fid in sorted(int(i) for i in ids):
        pcm = decode(audio_path(fid, "medium"))
        doc = {"fma_id": fid, "subset": "medium", "params": asdict(b2.DEFAULT),
               "n_samples": len(pcm), **landmarks(b2.fingerprint(pcm))}  # fmt: skip
        (OUT / f"{fid:06d}.json").write_text(json.dumps(doc, separators=(",", ":")) + "\n")
        print(fid, len(doc["hashes"]))


if __name__ == "__main__":
    main()
