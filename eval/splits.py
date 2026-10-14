"""Seeded, track-disjoint evaluation splits.

indexed:  tracks put in the index. val_pos and test_pos are disjoint subsets.
val_neg, test_neg: held-out tracks that are never indexed (negative queries).

Thresholds are tuned on val_* only. test_* is for reported numbers.
"""

import argparse
import json
from pathlib import Path

import numpy as np

from prototype.fma import load_tracks

MANIFEST_DIR = Path(__file__).resolve().parent / "manifests"


def make_splits(
    fma_ids: list[int], seed: int, n_neg: int, n_val_pos: int, n_test_pos: int
) -> dict[str, list[int]]:
    ids = np.array(sorted(fma_ids))
    if n_neg + n_val_pos + n_test_pos > len(ids):
        raise ValueError("split sizes exceed track count")
    perm = np.random.default_rng(seed).permutation(ids)
    neg, indexed = perm[:n_neg], perm[n_neg:]
    half = n_neg // 2
    return {
        "indexed": sorted(indexed.tolist()),
        "val_pos": sorted(indexed[:n_val_pos].tolist()),
        "test_pos": sorted(indexed[n_val_pos : n_val_pos + n_test_pos].tolist()),
        "val_neg": sorted(neg[:half].tolist()),
        "test_neg": sorted(neg[half:].tolist()),
    }


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--subset", default="small")
    ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--n-neg", type=int, default=1000)
    ap.add_argument("--n-val-pos", type=int, default=1000)
    ap.add_argument("--n-test-pos", type=int, default=1000)
    args = ap.parse_args()

    ids = [t.fma_id for t in load_tracks(args.subset)]
    splits = make_splits(ids, args.seed, args.n_neg, args.n_val_pos, args.n_test_pos)
    MANIFEST_DIR.mkdir(exist_ok=True)
    out = MANIFEST_DIR / f"splits_{args.subset}_seed{args.seed}.json"
    meta = {"subset": args.subset, "seed": args.seed, "n_tracks": len(ids)}
    out.write_text(json.dumps({"meta": meta, **splits}) + "\n")
    print(out, {k: len(v) for k, v in splits.items()})


if __name__ == "__main__":
    main()
