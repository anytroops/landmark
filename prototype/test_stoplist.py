import struct
from pathlib import Path

import numpy as np
import pytest

from prototype.stoplist import HEADER, MAGIC, StopList, crc32c


def write(path: Path, n_tracks: int, min_count: int, entries: list[tuple[int, int]],
          magic: bytes = MAGIC, bad_crc: bool = False) -> Path:  # fmt: skip
    body = HEADER.pack(magic, 1, 0, n_tracks, min_count, len(entries), 0)
    body += b"".join(struct.pack("<II", h, c) for h, c in entries)
    path.write_bytes(body + struct.pack("<I", crc32c(body) ^ int(bad_crc)))
    return path


def test_crc32c_check_value() -> None:
    # The standard CRC-32C check value, which Go's crc32.Castagnoli also gives.
    assert crc32c(b"123456789") == 0xE3069283


def test_load_count_keep(tmp_path: Path) -> None:
    sl = StopList.load(write(tmp_path / "s", 1000, 20, [(5, 20), (10, 50), (30, 21)]))
    assert (sl.n_tracks, sl.min_count, len(sl.hashes)) == (1000, 20, 3)
    np.testing.assert_array_equal(sl.count(np.array([1, 5, 10, 30, 99])), [0, 20, 50, 21, 0])
    limit = sl.limit(0.0215)  # floor(21.5) = 21
    assert limit == 21
    kept = sl.keep(np.array([1, 5, 10, 30]), limit)
    np.testing.assert_array_equal(kept, [True, True, False, True])


@pytest.mark.parametrize(
    ("fraction", "error"),
    [(0.019, "below the list's minimum"), (0.0, "positive"), (-1.0, "positive")],
)
def test_limit_rejects(tmp_path: Path, fraction: float, error: str) -> None:
    sl = StopList.load(write(tmp_path / "s", 1000, 20, [(5, 20)]))
    with pytest.raises(ValueError, match=error):
        sl.limit(fraction)


@pytest.mark.parametrize(
    ("kwargs", "entries", "error"),
    [
        ({"magic": b"NOPE"}, [(5, 20)], "magic"),
        ({"bad_crc": True}, [(5, 20)], "checksum"),
        ({}, [(10, 20), (5, 20)], "unsorted"),
        ({}, [(5, 19)], "below min_count"),
    ],
)
def test_load_rejects(
    tmp_path: Path, kwargs: dict, entries: list[tuple[int, int]], error: str
) -> None:
    with pytest.raises(ValueError, match=error):
        StopList.load(write(tmp_path / "s", 1000, 20, entries, **kwargs))


def test_empty_list_keeps_everything(tmp_path: Path) -> None:
    sl = StopList.load(write(tmp_path / "s", 1000, 20, []))
    assert sl.keep(np.array([1, 2, 3]), 20).all()
