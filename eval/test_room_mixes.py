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
    only_noise = render(x, "demand_pcafeter_snr0", np.random.default_rng(1))
    assert only_noise.shape == x.shape and not np.allclose(only_noise, x)
    for bad in ("hall", "", "_demand_pcafeter_snr0", "room_rt400_drr5_"):
        with pytest.raises(SystemExit):
            render(x, bad, np.random.default_rng(1))
