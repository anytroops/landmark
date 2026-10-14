"""In-memory inverted index in segment-v1 layout (D11).

keys     sorted unique uint32 hashes
offsets  len(keys) + 1 positions into the posting arrays
track    dense track id per posting, sorted by (key, track, t)
t        anchor frame per posting
cluster  per dense track: dense id of its duplicate cluster's representative
         (itself if it has no duplicates). Used by the acceptance rule (D14).

Same shape as the on-disk segment, minus varint encoding, so the Go port
translates rather than redesigns.
"""

from dataclasses import dataclass
from pathlib import Path

import numpy as np


@dataclass(frozen=True)
class Index:
    keys: np.ndarray  # uint32
    offsets: np.ndarray  # int64
    track: np.ndarray  # uint32
    t: np.ndarray  # uint32
    fma_ids: np.ndarray  # int64, dense id -> FMA id
    cluster: np.ndarray  # uint32, dense id -> cluster representative dense id

    @property
    def n_postings(self) -> int:
        return len(self.track)

    @property
    def n_tracks(self) -> int:
        return len(self.fma_ids)

    def save(self, path: Path) -> None:
        np.savez(
            path, keys=self.keys, offsets=self.offsets, track=self.track, t=self.t,
            fma_ids=self.fma_ids, cluster=self.cluster,
        )  # fmt: skip

    @classmethod
    def load(cls, path: Path) -> "Index":
        with np.load(path) as z:
            return cls(z["keys"], z["offsets"], z["track"], z["t"], z["fma_ids"], z["cluster"])


def dense_clusters(fma_ids: list[int], clusters: list[list[int]] | None) -> np.ndarray:
    """Map each dense id to the smallest dense id in its duplicate cluster."""
    dense = {f: i for i, f in enumerate(fma_ids)}
    out = np.arange(len(fma_ids), dtype=np.uint32)
    for members in clusters or []:
        ids = [dense[f] for f in members if f in dense]
        if ids:
            out[ids] = min(ids)
    return out


def build(
    fingerprints: list[tuple[np.ndarray, np.ndarray]],
    fma_ids: list[int],
    clusters: list[list[int]] | None = None,
) -> Index:
    """Build from per-track (hashes, t1) arrays. Position in the list is the dense id.

    `clusters` lists groups of FMA ids that are duplicates of each other.
    """
    if len(fingerprints) != len(fma_ids):
        raise ValueError("fingerprints and fma_ids differ in length")
    cluster = dense_clusters(fma_ids, clusters)
    ids = np.array(fma_ids, dtype=np.int64)
    lens = np.array([len(h) for h, _ in fingerprints], dtype=np.int64)
    if lens.sum() == 0:
        empty32 = np.empty(0, dtype=np.uint32)
        return Index(empty32, np.zeros(1, np.int64), empty32, empty32, ids, cluster)
    h = np.concatenate([h for h, _ in fingerprints]).astype(np.uint32)
    t = np.concatenate([t for _, t in fingerprints]).astype(np.uint32)
    track = np.repeat(np.arange(len(fingerprints), dtype=np.uint32), lens)

    order = np.lexsort((t, track, h))
    h, track, t = h[order], track[order], t[order]
    keys, starts = np.unique(h, return_index=True)
    offsets = np.append(starts, len(h)).astype(np.int64)
    return Index(keys, offsets, track, t, ids, cluster)
