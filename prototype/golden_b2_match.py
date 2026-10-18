"""Export golden B2 matcher results for the Go parity test (D31 option B2).

  python -m prototype.golden_b2_match

The index is the landmark sets already in testdata/golden_b2 (the FMA files
in name order, then synthetic.json), so Go builds the same index from the
same data. Queries are short excerpts under turntable, keylock, and noise
changes, fingerprinted here (Go has no resampler); each stores its query
landmarks and prototype.b2_match.search's full result. Go must reproduce
every candidate bit for bit (internal/match/triplet_test.go). Audio is not
exported.
"""

import json
from pathlib import Path

import numpy as np

from eval.degrade import add_noise, keylock, speed
from prototype import b2, b2_match
from prototype.audio import SAMPLE_RATE, decode
from prototype.fma import audio_path
from prototype.golden import synthetic_signal

ROOT = Path(__file__).resolve().parent.parent
DIR = ROOT / "testdata" / "golden_b2"
EXCERPT_S = 5.0


def index_files() -> list[Path]:
    return sorted(DIR.glob("[0-9]*.json")) + [DIR / "synthetic.json"]


def load(path: Path) -> b2.Landmarks:
    d = json.loads(path.read_text())
    return b2.Landmarks(np.array(d["hashes"], np.uint32), np.array(d["times"], np.uint32),
                        np.array(d["spans"], np.uint8), np.array(d["bands"], np.uint8))  # fmt: skip


def landmarks(lm: b2.Landmarks) -> dict[str, list[int]]:
    return {"hashes": lm.hash.tolist(), "times": lm.t.tolist(),
            "spans": lm.span.tolist(), "bands": lm.band.tolist()}  # fmt: skip


def main() -> None:
    files = index_files()
    index = b2_match.build([load(f) for f in files])
    fma = [int(f.stem) for f in files[:-1]]
    manifest = json.loads((ROOT / "eval/manifests/splits_medium_seed0.json").read_text())
    negative = int(sorted(manifest["test_neg"])[0])

    def clip(fma_id: int, start_s: float) -> np.ndarray:
        pcm = decode(audio_path(fma_id, "medium"))
        a = int(start_s * SAMPLE_RATE)
        return pcm[a : a + int(EXCERPT_S * SAMPLE_RATE)]

    synth = synthetic_signal(30 * SAMPLE_RATE)
    rng = np.random.default_rng(0)
    queries = [  # name, pcm, refine_tracks, top_k
        ("clean", clip(fma[0], 5), 20, 20),
        ("speed_+4pct", speed(clip(fma[0], 5), 1.04), 20, 20),
        ("speed_-8pct", speed(clip(fma[1], 12), 0.92), 20, 20),
        ("keylock_+6pct", keylock(clip(fma[2], 8), 1.06), 20, 20),
        ("speed_+12pct", speed(clip(fma[3], 3), 1.12), 20, 20),
        ("speed_-3pct_snr5", add_noise(speed(clip(fma[4], 15), 0.97), 5, rng), 20, 20),
        ("negative", clip(negative, 10), 20, 20),
        ("synthetic_speed_+5pct", speed(synth[10 * SAMPLE_RATE : 15 * SAMPLE_RATE], 1.05), 20, 20),
        ("speed_+4pct_refine2_top3", speed(clip(fma[0], 5), 1.04), 2, 3),
    ]
    out = []
    for name, pcm, refine, top_k in queries:
        q = b2.fingerprint_query(pcm)
        r = b2_match.search(index, q, top_k=top_k, refine_tracks=refine)
        out.append({"name": name, "refine_tracks": refine, "top_k": top_k, **landmarks(q),
                    "postings_scanned": r.postings_scanned, "votes": r.votes,
                    "candidates": [{"track": c.track, "score": c.score, "offset": c.offset,
                                    "scale": c.scale} for c in r.candidates]})  # fmt: skip
        top = r.candidates[0] if r.candidates else None
        print(f"{name}: {len(q.hash)} landmarks, {r.postings_scanned} matches, "
              f"top {top}")  # fmt: skip
    doc = {"index": [f.name for f in files],
           "params": {"scale_ratio": b2_match.SCALE_RATIO, "bin_min": b2_match.BIN_MIN,
                      "bin_max": b2_match.BIN_MAX, "table_bins": b2_match.TABLE_BINS,
                      "offset_frames": b2_match.OFFSET_FRAMES,
                      "near_frames": b2_match.NEAR_FRAMES,
                      "inlier_frames": b2_match.INLIER_FRAMES,
                      "max_bins_per_match": b2_match.MAX_BINS_PER_MATCH},
           "queries": out}  # fmt: skip
    (DIR / "match.json").write_text(json.dumps(doc, separators=(",", ":")) + "\n")


if __name__ == "__main__":
    main()
