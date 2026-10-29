import io
import json
import zipfile
from pathlib import Path

import numpy as np
import pytest
from scipy.io import wavfile

from eval import demand
from eval.degrade import add_noise
from eval.run_eval import condition

SR = 11025


def tone_wav(hz: float, seconds: float, rate: int = demand.SOURCE_RATE) -> bytes:
    t = np.arange(int(seconds * rate)) / rate
    buf = io.BytesIO()
    wavfile.write(buf, rate, (8000 * np.sin(2 * np.pi * hz * t)).astype(np.int16))
    return buf.getvalue()


def write_zips(root: Path, rate: int = demand.SOURCE_RATE) -> None:
    for env in demand.ENVIRONMENTS:
        with zipfile.ZipFile(root / f"{env}_16k.zip", "w") as z:
            z.writestr(f"{env}/ch01.wav", tone_wav(1000, 2.0, rate))
            z.writestr(f"{env}/ch02.wav", tone_wav(3000, 2.0, rate))


def test_prepare_takes_channel_one_and_resamples(tmp_path: Path) -> None:
    write_zips(tmp_path)
    out = tmp_path / "out"
    manifest = demand.prepare(tmp_path, out)
    assert set(manifest) == set(demand.ENVIRONMENTS)
    y = np.load(out / "PCAFETER.npy")
    assert y.dtype == np.float32 and abs(len(y) - 2 * SR) <= 1
    spec = np.abs(np.fft.rfft(y * np.hanning(len(y))))
    assert abs(np.argmax(spec) * SR / len(y) - 1000) < 2  # channel 1's tone, not channel 2's
    assert abs(float(np.max(np.abs(y))) - 8000 / 32768) < 0.01
    assert json.loads((out / "manifest.json").read_text())["TBUS"]["member"] == "TBUS/ch01.wav"


def test_prepare_rejects_another_rate(tmp_path: Path) -> None:
    write_zips(tmp_path, rate=48_000)
    with pytest.raises(ValueError, match="48000 Hz"):
        demand.prepare(tmp_path, tmp_path / "out")


def fake_recording(env: str) -> np.ndarray:
    """First half +1, second half -1: shows which half a window came from."""
    return np.concatenate([np.ones(30 * SR), -np.ones(30 * SR)]).astype(np.float32)


def test_splits_draw_from_disjoint_halves(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(demand, "recording", fake_recording)
    assert (demand.noise("TCAR", "val") == 1).all()
    assert (demand.noise("TCAR", "test") == -1).all()
    with pytest.raises(ValueError):
        demand.noise("TCAR", "train")


@pytest.mark.parametrize(("split", "sign"), [("val", 1), ("test", -1)])
@pytest.mark.parametrize("snr", [10, 0, -5])
def test_demand_condition_mixes_its_split_at_the_snr(
    monkeypatch: pytest.MonkeyPatch, split: str, sign: int, snr: int
) -> None:
    monkeypatch.setattr(demand, "recording", fake_recording)
    secs, degrade = condition(f"demand_pcafeter_snr{snr}_10s", split)
    assert secs == 10.0
    x = (0.3 * np.sin(np.arange(10 * SR) / 7)).astype(np.float32)
    added = degrade(x, np.random.default_rng(0)).astype(np.float64) - x
    assert (np.sign(added) == sign).all()
    snr_out = 10 * np.log10(np.mean(x.astype(np.float64) ** 2) / np.mean(added**2))
    assert abs(snr_out - snr) < 0.01


@pytest.mark.parametrize("name", ["demand_kitchen_snr0_10s", "demand_tcar_snrx_10s"])
def test_demand_condition_rejects_unknown_names(name: str) -> None:
    with pytest.raises(KeyError):
        condition(name)


def test_add_noise_takes_a_window_from_within_a_long_recording() -> None:
    x = np.ones(SR, dtype=np.float32)
    ramp = np.arange(3 * SR, dtype=np.float32)  # a seam would show as a drop
    for seed in range(50):
        added = add_noise(x, 0, np.random.default_rng(seed), ramp).astype(np.float64) - 1
        assert (np.diff(added) > 0).all()


def test_add_noise_tiles_a_short_recording() -> None:
    x = np.ones(SR, dtype=np.float32)
    short = np.array([1.0, -1.0], dtype=np.float32)
    y = add_noise(x, 0, np.random.default_rng(0), short)
    assert len(y) == SR
    assert abs(np.mean((y.astype(np.float64) - 1) ** 2) - 1) < 1e-6
