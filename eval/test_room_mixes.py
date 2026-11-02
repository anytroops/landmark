import numpy as np
import pytest

from eval.room_mixes import render
from prototype.audio import SAMPLE_RATE


def test_render_parses_room_and_noise(monkeypatch: pytest.MonkeyPatch) -> None:
    from eval import demand

    monkeypatch.setattr(demand, "noise", lambda env, split: np.ones(SAMPLE_RATE * 5))
    x = np.random.default_rng(0).standard_normal(SAMPLE_RATE * 3).astype(np.float32)
    dry = render(x, "room_rt400_drr5", np.random.default_rng(1))
    noisy = render(x, "room_rt400_drr5_demand_pcafeter_snr0", np.random.default_rng(1))
    assert dry.shape == noisy.shape == x.shape
    assert not np.allclose(dry, noisy)
    with pytest.raises(SystemExit):
        render(x, "hall", np.random.default_rng(1))
