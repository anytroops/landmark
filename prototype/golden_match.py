"""Export golden matcher results for the Go parity test (D22).

Index: the landmark sets in testdata/golden/*.json (sorted by file name;
dense id = position). Queries are built from those landmarks, no audio:
for track i, its landmarks with t in [100, 530) shifted back by 100 frames
(a true match at offset 100), plus track i+1's landmarks with t in [0, 430)
(a second, equally strong match at offset 0), so ranking and tie-breaking
get exercised. Expected output is prototype.match.search's ranking.
"""

import json
from pathlib import Path

import numpy as np

from prototype.index import build
from prototype.match import search

GOLDEN = Path(__file__).resolve().parent.parent / "testdata" / "golden"
TOP_K = 10


def load_sets() -> tuple[list[str], list[tuple[np.ndarray, np.ndarray]]]:
    names, sets = [], []
    for path in sorted(GOLDEN.glob("*.json")):
        if path.name == "match.json":
            continue
        g = json.loads(path.read_text())
        names.append(path.name)
        sets.append((np.array(g["hashes"], np.uint32), np.array(g["times"], np.uint32)))
    return names, sets


def make_query(sets: list[tuple[np.ndarray, np.ndarray]], i: int) -> tuple[np.ndarray, np.ndarray]:
    h, t = sets[i]
    keep = (t >= 100) & (t < 530)
    nh, nt = sets[(i + 1) % len(sets)]
    noise = nt < 430
    return (
        np.concatenate([h[keep], nh[noise]]),
        np.concatenate([t[keep] - 100, nt[noise]]).astype(np.uint32),
    )


def main() -> None:
    names, sets = load_sets()
    index = build(sets, list(range(len(sets))))
    queries = []
    for i in range(len(sets)):
        qh, qt = make_query(sets, i)
        res = search(index, qh, qt, top_k=TOP_K)
        queries.append(
            {
                "true_track": i,
                "postings_scanned": res.postings_scanned,
                "candidates": [[c.track, c.score, c.offset] for c in res.candidates],
            }
        )
    doc = {"tracks": names, "top_k": TOP_K, "queries": queries}
    (GOLDEN / "match.json").write_text(json.dumps(doc, indent=1) + "\n")
    print(f"{len(queries)} queries written")


if __name__ == "__main__":
    main()
