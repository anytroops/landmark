import numpy as np
import pytest
from scipy.signal import resample_poly

from eval.degrade import keylock, rir, room, speed, stretch
from eval.run_eval import condition
from prototype.audio import SAMPLE_RATE

SR = 11025


def tone(seconds: float = 2.0) -> np.ndarray:
    t = np.arange(int(seconds * SR)) / SR
    return (0.5 * np.sin(2 * np.pi * 440 * t)).astype(np.float32)


@pytest.mark.parametrize("factor", [1.02, 0.98])
def test_speed_matches_the_old_ratio_for_existing_conditions(factor: float) -> None:
    x = tone()
    old = resample_poly(x, 1000, int(round(1000 * factor))).astype(np.float32)
    np.testing.assert_array_equal(speed(x, factor), old)


@pytest.mark.parametrize("fn", [speed, stretch, keylock])
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
    assert abs(peak_hz(keylock(x, 1.05)) - 440) < 3
    assert abs(peak_hz(keylock(x, 0.95)) - 440) < 3
    assert abs(peak_hz(speed(x, 1.05)) - 462) < 3


@pytest.mark.parametrize(
    ("name", "secs"),
    [
        ("clean_10s", 10.0),
        ("speed_+0.25pct_10s", 10.0),
        ("stretch_-8pct_5s", 5.0),
        ("keylock_+3pct_10s", 10.0),
        ("white_snr-10_10s", 10.0),
        ("demand_tmetro_snr-5_5s", 5.0),
    ],
)
def test_condition_parses_names(name: str, secs: float) -> None:
    assert condition(name)[0] == secs


@pytest.mark.parametrize("name", ["speed_2pct_10s", "stretch_+x_10s", "warp_+1pct_10s"])
def test_condition_rejects_unknown_names(name: str) -> None:
    with pytest.raises(KeyError):
        condition(name)


@pytest.mark.parametrize("db", [-10, -5, 0])
def test_white_noise_at_any_snr(db: int) -> None:
    x = tone(10.0)
    added = condition(f"white_snr{db}_10s")[1](x, np.random.default_rng(3)).astype(np.float64) - x
    snr = 10 * np.log10(np.mean(x.astype(np.float64) ** 2) / np.mean(added**2))
    assert abs(snr - db) < 0.01


@pytest.mark.parametrize("rt60, drr", [(0.3, 0.0), (0.8, -5.0), (1.5, 5.0)])
def test_rir_has_its_reverberation_time_and_direct_ratio(rt60: float, drr: float) -> None:
    h = rir(rt60, drr, np.random.default_rng(0))
    tail = h[1:]
    assert 10 * np.log10(h[0] ** 2 / np.sum(tail**2)) == pytest.approx(drr, abs=1e-9)
    # Schroeder decay of the tail: -5 to -35 dB takes half the reverberation time.
    edc = 10 * np.log10(np.cumsum(tail[::-1] ** 2)[::-1] / np.sum(tail**2))
    t = np.arange(len(edc)) / SAMPLE_RATE
    use = (edc <= -5) & (edc >= -35)
    slope = np.polyfit(t[use], edc[use], 1)[0]
    assert -60 / slope == pytest.approx(rt60, rel=0.1)


def test_room_keeps_length_and_level_and_removes_the_low_end() -> None:
    t = np.arange(SAMPLE_RATE * 4) / SAMPLE_RATE
    x = (np.sin(2 * np.pi * 40 * t) + np.sin(2 * np.pi * 1000 * t)).astype(np.float32)
    y = room(x, 0.5, 20.0, np.random.default_rng(1))  # nearly dry: the phone's filter dominates
    assert len(y) == len(x) and y.dtype == np.float32
    assert np.mean(y.astype(np.float64) ** 2) == pytest.approx(np.mean(x.astype(np.float64) ** 2))
    spec = np.abs(np.fft.rfft(y[SAMPLE_RATE:]))
    freqs = np.fft.rfftfreq(len(y) - SAMPLE_RATE, 1 / SAMPLE_RATE)
    low, mid = spec[np.argmin(np.abs(freqs - 40))], spec[np.argmin(np.abs(freqs - 1000))]
    assert low / mid < 0.1  # a 150 Hz second-order high-pass: about -23 dB at 40 Hz
    np.testing.assert_array_equal(y, room(x, 0.5, 20.0, np.random.default_rng(1)))
    assert not np.array_equal(y, room(x, 0.5, 20.0, np.random.default_rng(2)))


def test_room_wraps_any_condition() -> None:
    secs, deg = condition("room_rt600_drr-5_white_snr0_10s")
    assert secs == 10.0
    x = np.random.default_rng(3).standard_normal(SAMPLE_RATE * 10).astype(np.float32)
    assert deg(x, np.random.default_rng(4)).shape == x.shape
    assert condition("room_rt600_drr0_clean_5s")[0] == 5.0
    with pytest.raises(KeyError):
        condition("room_rt600_drr0_nonsense_10s")
