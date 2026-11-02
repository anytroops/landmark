import pandas as pd
import pytest

from eval.dj_analysis import FPS, alignment


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
