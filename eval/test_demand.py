import io
import json
import zipfile
from pathlib import Path

import numpy as np
import pandas as pd
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


def test_band_shares_of_white_noise_follow_the_band_widths() -> None:
    x = np.random.default_rng(0).standard_normal(60 * SR)
    shares = demand.band_shares(x)
    edges = [0, *demand.BAND_EDGES_HZ, SR / 2]
    assert len(shares) == len(edges) - 1
    for share, lo, hi in zip(shares, edges[:-1], edges[1:], strict=True):
        assert abs(share - 100 * (hi - lo) / (SR / 2)) < 0.5
    assert abs(sum(shares) - 100) < 0.2


def test_report_b2_pairs_the_systems_on_the_same_queries(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    res = tmp_path / "eval" / "results"
    res.mkdir(parents=True)
    rows = []
    # Four queries: right in both, pairs only, triplets only, neither; one of
    # them right but under the triplet threshold, and a val row ignored.
    for fma_id, pairs_ok, b2_score in [(1, True, 50), (2, True, 10), (3, False, 60), (4, False, 0)]:
        for cond in ("white_snr0_10s", "demand_tcar_snr0_10s"):
            base = {"split": "test", "label": "pos", "fma_id": fma_id, "cond": cond}
            rows.append({**base, "system": "pairs", "top1_score": 30 if pairs_ok else 1,
                         "runner_up_score": 1, "correct": pairs_ok})  # fmt: skip
            rows.append({**base, "system": "b2", "top1_score": b2_score, "runner_up_score": 1,
                         "correct": b2_score > 0})  # fmt: skip
    val = {"split": "val", "label": "pos", "fma_id": 9, "cond": "white_snr0_10s"}
    rows.append({**val, "system": "b2", "top1_score": 99, "runner_up_score": 1, "correct": True})
    pd.DataFrame(rows).to_csv(res / "b2_eval_seed0_x.csv.gz", index=False)
    meta = {"catalog_tracks": 4, "systems": {
        "pairs": {"t_abs": 2, "t_ratio": 2.5, "test_far": 0.0},
        "b2": {"t_abs": 44, "t_ratio": 2.5, "test_far": 0.0}}}  # fmt: skip
    (res / "b2_eval_seed0_x.json").write_text(json.dumps(meta))
    monkeypatch.setattr(demand, "ROOT", tmp_path)
    r = demand.report_b2("_x", 0)
    c = r["accuracy"]["demand_tcar_snr0_10s"]
    assert c["n"] == 4 and c["pairs"] == 0.5 and c["b2"] == 0.5
    assert (c["only_b2"], c["only_pairs"]) == (1, 1)  # fma 3 vs fma 2 (score 10 < 44)
