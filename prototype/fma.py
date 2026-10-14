"""FMA metadata access: track listing, paths, and dense internal IDs."""

from dataclasses import dataclass
from pathlib import Path

import pandas as pd

DATA_DIR = Path(__file__).resolve().parent.parent / "data"


@dataclass(frozen=True)
class Track:
    fma_id: int
    title: str
    artist: str


def load_tracks(subset: str = "small", data_dir: Path = DATA_DIR) -> list[Track]:
    """Tracks in an FMA subset, sorted by FMA id.

    Subsets are nested: "small" is contained in "medium", which is in "large".
    """
    order = ["small", "medium", "large"]
    if subset not in order:
        raise ValueError(f"unknown subset {subset!r}")
    df = pd.read_csv(
        data_dir / "fma_metadata" / "tracks.csv", index_col=0, header=[0, 1], low_memory=False
    )
    allowed = set(order[: order.index(subset) + 1])
    df = df[df[("set", "subset")].isin(allowed)].sort_index()
    return [
        Track(int(tid), str(row[("track", "title")]), str(row[("artist", "name")]))
        for tid, row in df.iterrows()
    ]


def audio_path(fma_id: int, subset: str = "small", data_dir: Path = DATA_DIR) -> Path:
    tid = f"{fma_id:06d}"
    return data_dir / f"fma_{subset}" / tid[:3] / f"{tid}.mp3"
