import numpy as np
import pytest
from scipy.signal import resample_poly

from eval.degrade import speed, stretch
from eval.run_eval import condition

SR = 11025


def tone(seconds: float = 2.0) -> np.ndarray:
    t = np.arange(int(seconds * SR)) / SR
    return (0.5 * np.sin(2 * np.pi * 440 * t)).astype(np.float32)


@pytest.mark.parametrize("factor", [1.02, 0.98])
def test_speed_matches_the_old_ratio_for_existing_conditions(factor: float) -> None:
    x = tone()
    old = resample_poly(x, 1000, int(round(1000 * factor))).astype(np.float32)
    np.testing.assert_array_equal(speed(x, factor), old)


@pytest.mark.parametrize("fn", [speed, stretch])
@pytest.mark.parametrize("factor", [0.92, 0.9975, 1.005, 1.08])
def test_tempo_changes_scale_length(fn, factor: float) -> None:  # noqa: ANN001
    x = tone()
    y = fn(x, factor)
    assert np.isfinite(y).all()
    assert abs(len(y) - len(x) / factor) <= 0.002 * len(x)


def test_stretch_keeps_pitch_and_speed_shifts_it() -> None:
    def peak_hz(y: np.ndarray) -> float:
        spec = np.abs(np.fft.rfft(y * np.hanning(len(y))))
        return float(np.argmax(spec)) * SR / len(y)

    x = tone(4.0)
    assert abs(peak_hz(stretch(x, 1.05)) - 440) < 3
    assert abs(peak_hz(speed(x, 1.05)) - 462) < 3


@pytest.mark.parametrize(
    ("name", "secs"),
    [("clean_10s", 10.0), ("speed_+0.25pct_10s", 10.0), ("stretch_-8pct_5s", 5.0)],
)
def test_condition_parses_names(name: str, secs: float) -> None:
    assert condition(name)[0] == secs


@pytest.mark.parametrize("name", ["speed_2pct_10s", "stretch_+x_10s", "warp_+1pct_10s"])
def test_condition_rejects_unknown_names(name: str) -> None:
    with pytest.raises(KeyError):
        condition(name)
