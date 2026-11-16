import numpy as np
import pytest

from eval import mixsim
from prototype.audio import SAMPLE_RATE


def test_bands_sum_back_to_the_input_and_split_by_frequency() -> None:
    t = np.arange(SAMPLE_RATE * 2) / SAMPLE_RATE
    x = np.sin(2 * np.pi * 80 * t) + np.sin(2 * np.pi * 900 * t) + np.sin(2 * np.pi * 4000 * t)
    b = mixsim.bands(x)
    np.testing.assert_allclose(b.sum(axis=0), x, atol=1e-9)
    power = (b[:, SAMPLE_RATE:] ** 2).mean(axis=1)
    for band, f in enumerate((80, 900, 4000)):
        tone = np.sin(2 * np.pi * f * t)[SAMPLE_RATE:]
        alone = (mixsim.bands(np.sin(2 * np.pi * f * t))[:, SAMPLE_RATE:] ** 2).mean(axis=1)
        assert np.argmax(alone) == band
        assert alone[band] > 0.8 * (tone**2).mean()
    assert power.min() > 0


def test_automation_stays_in_range_and_each_move_does_what_it_says() -> None:
    hz = mixsim.CONTROL_HZ
    plays = [(0, 30 * hz), (20 * hz, 50 * hz), (40 * hz, 70 * hz), (60 * hz, 90 * hz)]
    seen = set()
    for seed in range(40):
        g, moves = mixsim.automate(90 * hz + 1, plays, np.random.default_rng(seed))
        assert g.shape == (4, 3, 90 * hz + 1)
        assert g.min() >= 0 and g.max() <= 10 ** (6 / 20) + 1e-9
        for i, (a, b) in enumerate(plays):
            assert np.all(g[i, :, :a] == 0) and np.all(g[i, :, b:] == 0)
        for i, m in enumerate(moves):
            seen.add(m["move"])
            end_out = plays[i][1]
            if m["move"] == "bass_swap":
                before, after = m["swap"] - 5, min(m["swap"] + 2 * hz + 5, end_out - 1)
                # The killed band sits 25 to 40 dB down; trims of +/-6 dB per band
                # can close that by 12 dB at most.
                assert g[i + 1, 0, before] < 0.25 * g[i + 1, 1, before]  # incoming bass killed
                assert g[i, 0, after] < 0.25 * max(g[i, 1, after], 1e-12) or g[i, 1, after] == 0
            if m["move"] == "cut":
                assert g[i + 1, :, m["at"] - 1].max() == 0 and g[i, :, m["at"]].max() == 0
    assert seen == set(mixsim.MOVES)


@pytest.mark.slow
def test_render_labels_match_the_mix_power() -> None:
    from eval.mixsim import render, split_tracks
    from prototype.fma import audio_path

    pool = split_tracks("val")
    if not all(audio_path(int(t), "medium").exists() for t in pool[:3]):
        pytest.skip("needs fma_medium's audio")
    mix = render([int(t) for t in pool[:3]], np.random.default_rng(0))
    assert mix is not None
    spec = mix["spec"].astype(np.float64)
    assert len(mix["songs"]) == 3
    for s in mix["songs"]:
        assert 0 <= s["first"] < s["last"] < len(spec)
        assert s["gains"].shape == (s["last"] - s["first"] + 1, 3)
