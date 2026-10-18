import numpy as np
import pytest

from eval import tracklist
from eval.backends import Hit
from eval.tracklist import hypotheses, rescale
from prototype.fingerprint import DT_BITS, F_BITS, unpack


def pack(f1: int, f2: int, dt: int) -> int:
    return (f1 << (F_BITS + DT_BITS)) | (f2 << DT_BITS) | dt


def test_hypotheses_grid() -> None:
    hyps = hypotheses(max_pct=2.0, step_pct=1.0, modes=["speed", "keylock"])
    assert hyps[0] == ("none", 1.0)
    assert len(hyps) == 1 + 2 * 2 * 2  # identity + 2 modes x 2 steps x 2 signs
    assert ("speed", 1.02) in hyps and ("keylock", 0.98) in hyps
    extra = hypotheses(max_pct=1.0, step_pct=1.0, modes=["speed"], extra=(2.0, 0.5))
    assert extra == [
        ("none", 1.0),
        ("speed", 1.01),
        ("speed", 0.99),
        ("speed", 2.0),
        ("speed", 0.5),
    ]


def test_rescale_identity() -> None:
    h, t = np.array([pack(100, 110, 20)], np.uint32), np.array([5], np.uint32)
    h2, t2 = rescale(h, t, "none", 1.0)
    assert h2 is h and t2 is t


@pytest.mark.parametrize(
    ("mode", "factor", "want"),
    [
        # keylock 2% faster: reference gaps and times are 2% longer, pitch unchanged
        ("keylock", 1.02, (pack(100, 110, 41), 102)),
        # turntable 2% faster: also undo the 2% pitch rise
        ("speed", 1.02, (pack(98, 108, 41), 102)),
    ],
)
def test_rescale_undoes_a_tempo_change(mode: str, factor: float, want: tuple[int, int]) -> None:
    h, t = np.array([pack(100, 110, 40)], np.uint32), np.array([100], np.uint32)
    h2, t2 = rescale(h, t, mode, factor)
    assert (int(h2[0]), int(t2[0])) == want


def test_rescale_drops_gaps_outside_the_hash_range() -> None:
    h = np.array([pack(100, 110, 62), pack(100, 110, 12)], np.uint32)
    t = np.array([0, 1], np.uint32)
    h2, _ = rescale(h, t, "keylock", 1.05)  # 62 * 1.05 = 65.1 > 63 drops; 12.6 rounds to 13
    assert unpack(h2)[2].tolist() == [13]


def test_b2_hypotheses() -> None:
    assert tracklist.b2_hypotheses(["speed", "keylock"], (2.0, 0.5)) == [
        ("b2", 1.0),
        ("b2_speed", 2.0),
        ("b2_speed", 0.5),
        ("b2_keylock", 2.0),
        ("b2_keylock", 0.5),
    ]
    assert tracklist.b2_hypotheses(["speed"]) == [("b2", 1.0)]


class _FakeShard:
    def __init__(self) -> None:
        self.calls: list[tuple[int, int, int, int]] = []

    def search(self, h, t, spans, bands):  # noqa: ANN001, ANN201
        self.calls.append((len(h), len(t), len(spans), len(bands)))
        return Hit(5, 100, 40, 3, 999, 1.03)


def test_search_b2_reports_the_fitted_tempo(monkeypatch: pytest.MonkeyPatch) -> None:
    fake = _FakeShard()
    monkeypatch.setattr(tracklist, "_STATE", {"backend": fake, "stop": None, "limit": None})
    x = np.random.default_rng(0).standard_normal(3 * 11025).astype(np.float32)
    rows = tracklist._search((0, 1.0, x, [("b2", 1.0), ("b2_speed", 2.0)], 1, "b2"))
    assert [r["mode"] for r in rows] == ["b2", "b2_speed"]
    assert rows[0]["factor"] == pytest.approx(1.03) and rows[1]["factor"] == pytest.approx(2.06)
    assert all(r["offset"] == 40 and r["top1_dense"] == 5 for r in rows)
    assert all(c[0] > 0 and len(set(c)) == 1 for c in fake.calls)  # parallel arrays, one length
