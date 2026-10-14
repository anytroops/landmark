"""Seeded, duplicate-aware evaluation splits (D12).

Units of splitting are duplicate clusters (eval/dedupe.py), not tracks, so a
negative never has a copy in the index and val/test never share audio.

indexed:            tracks put in the index
val_pos, test_pos:  query tracks, disjoint clusters, one track per cluster, all indexed
val_neg, test_neg:  held-out tracks, never indexed (negative queries)
clusters:           multi-track clusters among the tracks above; a query is
                    correct if its top-1 is in the same cluster

Undecodable tracks are excluded. Thresholds are tuned on val_* only.
"""

import argparse
import json
from pathlib import Path

import numpy as np

from prototype.fma import load_tracks

MANIFEST_DIR = Path(__file__).resolve().parent / "manifests"


def make_splits(
    groups: list[list[int]], seed: int, n_neg: int, n_val_pos: int, n_test_pos: int
) -> dict[str, list[int]]:
    """Split groups of track ids. Negative sets may overshoot by one group's size."""
    rng = np.random.default_rng(seed)
    groups = [sorted(g) for g in sorted(groups)]
    order = rng.permutation(len(groups))
    val_neg: list[int] = []
    test_neg: list[int] = []
    indexed_groups: list[list[int]] = []
    for gi in order:
        g = groups[gi]
        if len(val_neg) < n_neg // 2:
            val_neg += g
        elif len(test_neg) < n_neg - n_neg // 2:
            test_neg += g
        else:
            indexed_groups.append(g)
    if len(indexed_groups) < n_val_pos + n_test_pos:
        raise ValueError("not enough indexed clusters for the positive splits")
    reps = [int(g[rng.integers(len(g))]) for g in indexed_groups]
    return {
        "indexed": sorted(t for g in indexed_groups for t in g),
        "val_pos": sorted(reps[:n_val_pos]),
        "test_pos": sorted(reps[n_val_pos : n_val_pos + n_test_pos]),
        "val_neg": sorted(val_neg),
        "test_neg": sorted(test_neg),
    }


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--subset", default="small")
    ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--n-neg", type=int, default=1000)
    ap.add_argument("--n-val-pos", type=int, default=1000)
    ap.add_argument("--n-test-pos", type=int, default=1000)
    args = ap.parse_args()

    dup = json.loads((MANIFEST_DIR / f"duplicates_{args.subset}.json").read_text())
    bad = set(dup["meta"]["decode_failures"])
    ids = [t.fma_id for t in load_tracks(args.subset) if t.fma_id not in bad]
    clustered = {t for members in dup["clusters"].values() for t in members}
    groups = [[t for t in m if t not in bad] for m in dup["clusters"].values()]
    groups = [g for g in groups if g] + [[t] for t in ids if t not in clustered]

    splits = make_splits(groups, args.seed, args.n_neg, args.n_val_pos, args.n_test_pos)
    clusters = {str(min(g)): g for g in groups if len(g) > 1}
    MANIFEST_DIR.mkdir(exist_ok=True)
    out = MANIFEST_DIR / f"splits_{args.subset}_seed{args.seed}.json"
    meta = {
        "subset": args.subset,
        "seed": args.seed,
        "n_tracks": len(ids),
        "excluded_undecodable": sorted(bad),
        "duplicate_threshold": dup["meta"]["threshold"],
    }
    out.write_text(json.dumps({"meta": meta, **splits, "clusters": clusters}) + "\n")
    print(out, {k: len(v) for k, v in splits.items()}, "clusters:", len(clusters))


if __name__ == "__main__":
    main()
