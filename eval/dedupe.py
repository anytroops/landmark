"""Find duplicate / shared-audio track clusters in an FMA subset (D12).

FMA contains re-uploads, radio edits, clean versions, and instrumentals of
the same recording. Left alone, a "never indexed" negative can have a copy in
the index and its correct match counts as a false accept.

Method: index every track, query each track's full clip, record other tracks
scoring >= RECORD_MIN. Pairs at or above --threshold are unioned into
clusters. Pairs are written so the threshold choice can be audited.
"""

import argparse
import json
import logging
import os
from pathlib import Path

import pandas as pd

from prototype.build_index import fingerprint_tracks
from prototype.fma import load_tracks
from prototype.index import build
from prototype.match import search

ROOT = Path(__file__).resolve().parent.parent
RECORD_MIN = 15
TOP_K = 20
log = logging.getLogger("dedupe")


def clusters_from_pairs(ids: list[int], pairs: list[tuple[int, int]]) -> dict[int, int]:
    """Union-find. Maps every id to its cluster's smallest id."""
    parent = {i: i for i in ids}

    def find(x: int) -> int:
        while parent[x] != x:
            parent[x] = parent[parent[x]]
            x = parent[x]
        return x

    for a, b in pairs:
        ra, rb = find(a), find(b)
        if ra != rb:
            parent[max(ra, rb)] = min(ra, rb)
    return {i: find(i) for i in ids}


def query_all(ids: list[int], subset: str, workers: int | None) -> tuple[pd.DataFrame, list[int]]:
    results, failures = fingerprint_tracks(ids, subset, workers)
    ok = sorted(results)
    index = build([results[f][:2] for f in ok], ok)
    rows = []
    for i, fid in enumerate(ok):
        h, t, _ = results[fid]
        for c in search(index, h, t, top_k=TOP_K).candidates:
            other = int(index.fma_ids[c.track])
            if other != fid and c.score >= RECORD_MIN:
                rows.append({"a": fid, "b": other, "score": c.score, "offset": c.offset})
        if (i + 1) % 1000 == 0:
            log.info("%d/%d queried", i + 1, len(ok))
    pairs = pd.DataFrame(rows, columns=["a", "b", "score", "offset"])
    return pairs, [int(f["fma_id"]) for f in failures]


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--subset", default="small")
    ap.add_argument("--threshold", type=int, required=True)
    ap.add_argument("--workers", type=int, default=os.cpu_count())
    ap.add_argument("--from-pairs", action="store_true", help="re-cluster saved pairs only")
    args = ap.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")

    ids = [t.fma_id for t in load_tracks(args.subset)]
    pairs_path = ROOT / "eval" / "results" / f"dup_pairs_{args.subset}.csv"
    if args.from_pairs:
        pairs = pd.read_csv(pairs_path)
        failures = json.loads(
            (ROOT / "eval" / "manifests" / f"duplicates_{args.subset}.json").read_text()
        )["meta"]["decode_failures"]
    else:
        pairs, failures = query_all(ids, args.subset, args.workers)
        pairs.to_csv(pairs_path, index=False)
    strong = pairs[pairs.score >= args.threshold]
    cluster = clusters_from_pairs(ids, list(zip(strong.a, strong.b, strict=True)))
    sizes = pd.Series(cluster).value_counts()
    multi = {
        int(k): sorted(int(i) for i, c in cluster.items() if c == k) for k in sizes[sizes > 1].index
    }
    out = {
        "meta": {
            "subset": args.subset,
            "threshold": args.threshold,
            "record_min": RECORD_MIN,
            "decode_failures": failures,
            "tracks_in_multi_clusters": int(sizes[sizes > 1].sum()),
        },
        "clusters": multi,
    }
    path = ROOT / "eval" / "manifests" / f"duplicates_{args.subset}.json"
    path.write_text(json.dumps(out, indent=1) + "\n")
    scores = pairs.score.to_numpy()
    log.info("pairs >= %d: %d; histogram of pair scores:", RECORD_MIN, len(pairs))
    for lo, hi in [(15, 20), (20, 30), (30, 40), (40, 60), (60, 100), (100, 200), (200, 10**9)]:
        log.info(
            "  [%d, %s): %d",
            lo,
            hi if hi < 10**9 else "inf",
            int(((scores >= lo) & (scores < hi)).sum()),
        )
    log.info(
        "clusters: %d, tracks in them: %d", len(multi), out["meta"]["tracks_in_multi_clusters"]
    )
    log.info("wrote %s", path)


if __name__ == "__main__":
    main()
