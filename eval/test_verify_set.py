import numpy as np
import pytest

from eval.dj_analysis import FPS, MARGIN
from eval.verify_set import score_alignment, starts, window_corr


def spec(frames: int, seed: int) -> np.ndarray:
    return np.random.default_rng(seed).normal(0, 6, (frames, 234)).astype(np.float32)


def test_right_alignment_correlates_fully_and_a_wrong_one_does_not() -> None:
    ref = spec(4000, 0)
    n = int(10 * FPS)
    a = 20.0
    mix = ref[int(a * FPS) :]
    assert window_corr(mix, ref, 100, n, a, 1.0, 0) == pytest.approx(1.0, abs=0.01)
    assert abs(window_corr(mix, ref, 100, n, a + 7.3, 1.0, 0)) < 0.1
    assert abs(window_corr(mix, spec(4000, 1), 100, n, a, 1.0, 0)) < 0.1


def test_a_turntable_pitch_is_found_by_shifting_bands() -> None:
    ref = spec(4000, 2)
    shift = 3
    mix = np.zeros_like(ref)
    mix[:, MARGIN + shift : 234 - MARGIN + shift] = ref[:, MARGIN : 234 - MARGIN]
    rate = 2 ** (shift / 36)
    n = int(10 * FPS)
    resampled = np.stack([np.interp(np.arange(2000) * rate, np.arange(4000), mix[:, b])
                          for b in range(234)], axis=1).astype(np.float32)  # fmt: skip
    corr, pitch = score_alignment(resampled, ref, [50], 0.0, rate)
    assert pitch is True and corr > 0.5
    assert np.isnan(window_corr(resampled, ref, 3000, n, 200.0, rate, shift))


def test_windows_cover_the_entry_and_at_least_one_exists() -> None:
    total = int(60 * FPS)
    assert len(starts(0.0, 3.0, total)) == 1
    w = starts(5.0, 40.0, total)
    assert w[0] == int(5 * FPS) and len(w) > 4 and w[-1] + int(10 * FPS) <= int(40 * FPS) + 1
