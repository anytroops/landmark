import numpy as np
import pytest

from prototype import mixer


def spectra(frames: int, seed: int) -> np.ndarray:
    return np.random.default_rng(seed).uniform(0.1, 10.0, (frames, 234))


def mix_of(sources: list[np.ndarray], g: np.ndarray) -> np.ndarray:
    out = np.zeros_like(sources[0])
    for k, s in enumerate(sources):
        for b, sl in enumerate(mixer.band_slices()):
            out[:, sl] += (g[k, :, b] ** 2)[:, None] * s[:, sl]
    return out


def test_b1_recovers_per_band_gains_exactly_without_smoothing() -> None:
    n = 200
    sources = [spectra(n, 0), spectra(n, 1)]
    sources[1][:50] = 0.0  # the second song comes in at frame 50
    g = np.zeros((2, n, 3))
    g[0] = np.linspace([1.0, 1.0, 0.8], [0.2, 0.9, 0.05], n)
    g[1, 50:] = np.linspace([0.01, 0.3, 0.3], [1.0, 1.0, 1.2], n - 50)
    est = mixer.fit_b1(mix_of(sources, g), sources, smooth=False)
    np.testing.assert_allclose(est, g, atol=1e-6)
    assert mixer.reconstruction_error(mix_of(sources, g), sources, est) < 1e-10


def test_smoothed_b1_stays_off_where_a_song_does_not_play() -> None:
    n = 120
    sources = [spectra(n, 2), spectra(n, 3)]
    sources[1][:60] = 0.0
    g = np.ones((2, n, 3))
    g[1, :60] = 0.0
    est = mixer.fit_b1(mix_of(sources, g), sources)
    assert np.all(est[1, :60] == 0.0)
    assert np.allclose(est[0, 10:-10], 1.0, atol=1e-6)


def test_features_and_db() -> None:
    n = 50
    sources = [spectra(n, 4), spectra(n, 5)]
    g = np.full((2, n, 3), 0.5)
    f = mixer.features(mix_of(sources, g), sources, g, 0)
    assert f.shape == (n, 75)
    np.testing.assert_allclose(f[:, 72:], 20 * np.log10(0.5) / 20, atol=1e-6)
    assert mixer.to_db(np.array([0.0, 1.0]))[0] == mixer.FLOOR_DB


def test_model_starts_as_b1_and_keeps_its_shape() -> None:
    torch = pytest.importorskip("torch")
    from prototype.mixer_model import IN_CHANNELS, MixerTCN

    x = torch.randn(2, IN_CHANNELS, 300) * 0.3
    m = MixerTCN(ch=16, layers=3)
    y = m(x)
    assert y.shape == (2, 3, 300)
    expect = torch.clamp(x[:, 72:75], mixer.FLOOR_DB / 20, 12 / 20)
    assert torch.allclose(y, expect)
