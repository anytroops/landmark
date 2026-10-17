"""Read a stop list written by the Go stack (D30) and drop uninformative landmarks.

Same format and rule as internal/stoplist: (hash, global posting count) for
every hash with at least min_count postings, sorted by hash, with a CRC-32C
footer. A query landmark is dropped when its hash's count exceeds
floor(fraction x n_tracks); unlisted hashes have fewer than min_count
postings and are always kept.
"""

import math
import struct
from dataclasses import dataclass
from functools import cache
from pathlib import Path

import numpy as np

MAGIC = b"LMST"
VERSION = 1
# magic, version, reserved, n_tracks, min_count, n_entries, reserved
HEADER = struct.Struct("<4sHHIIII")
FOOTER = 4


@cache
def _crc32c_table() -> tuple[int, ...]:
    table = []
    for i in range(256):
        c = i
        for _ in range(8):
            c = (c >> 1) ^ 0x82F63B78 if c & 1 else c >> 1
        table.append(c)
    return tuple(table)


def crc32c(data: bytes) -> int:
    """CRC-32C (Castagnoli), as Go's crc32.Checksum with crc32.Castagnoli."""
    table = _crc32c_table()
    c = 0xFFFFFFFF
    for b in data:
        c = table[(c ^ b) & 0xFF] ^ (c >> 8)
    return c ^ 0xFFFFFFFF


@dataclass(frozen=True)
class StopList:
    n_tracks: int
    min_count: int
    hashes: np.ndarray  # uint32, strictly increasing
    counts: np.ndarray  # uint32, each >= min_count
    crc: int

    @classmethod
    def load(cls, path: Path) -> "StopList":
        b = path.read_bytes()
        if len(b) < HEADER.size + FOOTER:
            raise ValueError(f"{path}: {len(b)} bytes is too short for a stop list")
        magic, version, _, n_tracks, min_count, n, _ = HEADER.unpack_from(b)
        if magic != MAGIC or version != VERSION:
            raise ValueError(f"{path}: magic {magic!r} version {version}, want {MAGIC!r} {VERSION}")
        body = b[:-FOOTER]
        crc = crc32c(body)
        (want,) = struct.unpack_from("<I", b, len(b) - FOOTER)
        if crc != want:
            raise ValueError(f"{path}: checksum {crc:08x}, footer says {want:08x}")
        if len(body) != HEADER.size + 8 * n:
            raise ValueError(
                f"{path}: {n} entries need {HEADER.size + 8 * n} bytes, have {len(body)}"
            )
        pairs = np.frombuffer(body, dtype="<u4", offset=HEADER.size).reshape(n, 2)
        hashes, counts = pairs[:, 0].copy(), pairs[:, 1].copy()
        unsorted = n > 1 and bool(np.any(np.diff(hashes.astype(np.int64)) <= 0))
        if min_count < 1 or unsorted or (n and counts.min() < min_count):
            raise ValueError(f"{path}: entries unsorted or below min_count {min_count}")
        return cls(n_tracks, min_count, hashes, counts, crc)

    def limit(self, fraction: float) -> int:
        """Largest posting count a hash may have and stay in a query."""
        if not fraction > 0:
            raise ValueError(f"cutoff fraction {fraction} must be positive")
        limit = math.floor(fraction * self.n_tracks)
        if limit < self.min_count:
            raise ValueError(
                f"cutoff {fraction} x {self.n_tracks} tracks = {limit} postings is below "
                f"the list's minimum {self.min_count}"
            )
        return limit

    def count(self, hashes: np.ndarray) -> np.ndarray:
        """Global posting count per hash; 0 for hashes not listed."""
        hashes = np.asarray(hashes, dtype=np.uint32)
        if len(self.hashes) == 0:
            return np.zeros(len(hashes), dtype=np.uint32)
        i = np.searchsorted(self.hashes, hashes)
        j = np.minimum(i, len(self.hashes) - 1)
        found = (i < len(self.hashes)) & (self.hashes[j] == hashes)
        return np.where(found, self.counts[j], 0).astype(np.uint32)

    def keep(self, hashes: np.ndarray, limit: int) -> np.ndarray:
        """Boolean mask of the landmarks a query keeps under limit."""
        return self.count(hashes) <= limit
