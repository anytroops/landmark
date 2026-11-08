import numpy as np
import pytest

from eval import phone_sim as ps

V = ps.VENUES["small_club"]


def test_direct_sound_comes_first_at_unit_gain_with_reflections_after() -> None:
    h = ps.early_reflections(V)
    assert h[0] == pytest.approx(1.0, abs=0.2)  # other paths may land on the same sample
    assert np.count_nonzero(h) > 10
    assert np.all(h[1:] <= 1.0)


def test_tail_has_the_venues_energy_and_starts_late() -> None:
    tail = ps.late_tail(V, np.random.default_rng(0))
    assert np.sum(tail**2) == pytest.approx(1.0 / ps.direct_to_reverb(V))
    assert np.all(tail[: int(ps.LATE_START_S * ps.RATE)] == 0.0)


def test_tail_decays_at_the_reverberation_time_and_highs_die_first() -> None:
    from scipy.signal import butter, sosfilt

    tail = ps.late_tail(V, np.random.default_rng(1))

    def decay_s(band: list[float]) -> float:
        y = sosfilt(butter(4, band, "bandpass", fs=ps.RATE, output="sos"), tail)
        edc = 10 * np.log10(np.cumsum(y[::-1] ** 2)[::-1] / np.sum(y**2))
        t = np.arange(len(edc)) / ps.RATE
        use = (edc <= -5) & (edc >= -25)
        return -60 / np.polyfit(t[use], edc[use], 1)[0]

    mid, high = decay_s([1000, 2000]), decay_s([6000, 10000])
    assert mid == pytest.approx(V.rt60, rel=0.2)
    assert high < 0.75 * mid


def test_automatic_gain_narrows_the_dynamics_and_never_clips() -> None:
    n = ps.RATE * 2
    x = np.concatenate([0.9 * np.ones(n // 2), 0.05 * np.ones(n // 2)])
    y = ps.automatic_gain(x)
    assert len(y) == len(x) and np.max(np.abs(y)) <= 1.0
    rms = lambda s: np.sqrt(np.mean(s**2))  # noqa: E731
    before = rms(x[n // 4 : n // 2]) / rms(x[3 * n // 4 :])
    after = rms(y[n // 4 : n // 2]) / rms(y[3 * n // 4 :])
    assert after < 0.6 * before


def test_phone_recording_is_seeded_and_keeps_length(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(
        ps.demand, "noise", lambda env, split: np.random.default_rng(5).standard_normal(50_000)
    )
    x = np.random.default_rng(2).standard_normal(ps.RATE * 3)
    a = ps.phone_recording(x, V, np.random.default_rng(3))
    assert len(a) == len(x) and np.max(np.abs(a)) <= 1.0
    np.testing.assert_array_equal(a, ps.phone_recording(x, V, np.random.default_rng(3)))
    assert not np.array_equal(a, ps.phone_recording(x, V, np.random.default_rng(4)))


@pytest.mark.parametrize("name", sorted(ps.VENUES))
def test_venues_have_plausible_direct_to_reverb(name: str) -> None:
    drr_db = 10 * np.log10(ps.direct_to_reverb(ps.VENUES[name]))
    assert -12 < drr_db < 6
