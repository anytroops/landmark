import numpy as np
import pytest

from eval.mixgen import pink_noise, shelf

SR = 11025


def gain_db(fn, hz: float) -> float:  # noqa: ANN001
    t = np.arange(SR * 2) / SR
    x = np.sin(2 * np.pi * hz * t).astype(np.float32)
    y = fn(x)[SR:]  # skip the filter's start-up
    return 20 * np.log10(np.sqrt(np.mean(y**2)) / np.sqrt(np.mean(x[SR:] ** 2)))


@pytest.mark.parametrize(
    ("high", "freq", "boosted_hz", "untouched_hz"),
    [(False, 200, 40, 3000), (True, 3000, 5000, 100)],
)
def test_shelf_boosts_one_side_only(high: bool, freq: float, boosted_hz: float,
                                    untouched_hz: float) -> None:  # fmt: skip
    for g in (6.0, -6.0):
        fn = lambda x, g=g: shelf(x, g, freq, high, SR)  # noqa: E731
        assert abs(gain_db(fn, boosted_hz) - g) < 1.0
        assert abs(gain_db(fn, untouched_hz)) < 0.5


def test_pink_noise_falls_with_frequency() -> None:
    x = pink_noise(SR * 8, np.random.default_rng(0))
    p = np.abs(np.fft.rfft(x)) ** 2
    f = np.fft.rfftfreq(len(x), 1 / SR)
    low, high = p[(f > 50) & (f < 100)].mean(), p[(f > 3200) & (f < 6400)].mean()
    assert 10 * np.log10(low / high) > 15  # 1/f: about 18 dB over 6 octaves
