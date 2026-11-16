import numpy as np
import pandas as pd
import pytest

from eval.dj_analysis import FPS, alignment, merge_aligned
from eval.tracklist_score import Entry


def rows(intercepts: list[float], scores: list[float] | None = None) -> pd.DataFrame:
    scores = scores or [100.0] * len(intercepts)
    return pd.DataFrame([
        {"t0": 2.5 * i, "mode": "b2", "factor": 1.0, "top1_dense": 7, "score": s,
         "offset": (a + 2.5 * i) * FPS}
        for i, (a, s) in enumerate(zip(intercepts, scores, strict=True))
    ])  # fmt: skip


def test_consensus_takes_the_intercept_most_windows_share() -> None:
    r = rows([30.0, 2.4, 18.6, 2.41, 6.2, 2.39])
    a, rate = alignment(r, 7, 0, 20)
    assert rate == 1.0
    assert a == pytest.approx(2.4)
    assert alignment(r, 7, 0, 20, "median")[0] == pytest.approx((2.41 + 6.2) / 2)


def test_consensus_ties_go_to_the_larger_total_score() -> None:
    r = rows([10.0, 10.0, 20.0, 20.0], [50, 50, 90, 90])
    assert alignment(r, 7, 0, 10)[0] == pytest.approx(20.0)
    assert alignment(r, 8, 0, 10) is None


def test_merge_aligned_joins_pieces_of_one_play() -> None:
    a1, r1 = 8.0, 0.93
    rows = pd.DataFrame(
        [{"t0": t0, "mode": "b2", "factor": r1, "top1_dense": 1, "score": 50,
          "offset": (a1 + r1 * t0) * FPS} for t0 in np.arange(0, 120, 2.5)]
        + [{"t0": t0, "mode": "b2", "factor": 1.0, "top1_dense": 3, "score": 50,
            "offset": (a + t0) * FPS} for t0, a in ((130, 20), (132.5, 20), (150, 70), (152.5, 70))]
    )  # fmt: skip
    entries = [Entry(1, 0, 38, r1, 100), Entry(2, 40, 50, 1, 20), Entry(1, 52, 80, r1, 300),
               Entry(1, 95, 120, r1, 200), Entry(3, 128, 140, 1, 40),
               Entry(3, 148, 160, 1, 40)]  # fmt: skip
    got = merge_aligned(entries, rows)
    assert [(e.track, e.start, e.end, e.evidence) for e in got] == [
        (1, 0, 120, 300), (2, 40, 50, 20), (3, 128, 140, 40), (3, 148, 160, 40)]  # fmt: skip
    assert len(merge_aligned(entries, rows, gap=14)) == 5  # 52 is 14 past 38; 95 is 15 past 80
    assert entries[0].end == 38  # the input is left alone
