import json

import pandas as pd
import pytest

from eval import b2_compare


def _run(res, tag, rows, t_abs, conds=None, params=None):
    pd.DataFrame(rows).to_csv(res / f"b2_eval_seed0{tag}.csv.gz", index=False)
    b2 = {"t_abs": t_abs, "t_ratio": 2.0, "conditions": conds or {}}
    meta = {"b2_params": params or {}, "reported_split": "test", "systems": {"b2": b2}}
    (res / f"b2_eval_seed0{tag}.json").write_text(json.dumps(meta))


def _row(fma_id, cond, score, correct=True, label="pos", split="test"):
    return {"split": split, "label": label, "fma_id": fma_id, "cond": cond, "system": "b2",
            "top1_score": score, "runner_up_score": 5, "correct": correct}  # fmt: skip


def test_pair_counts_each_side_at_its_own_threshold(tmp_path, monkeypatch):
    monkeypatch.setattr(b2_compare, "RES", tmp_path)
    c = "demand_tcar_snr0_10s"
    # Run A accepts at 20, run B at 40: track 2 (score 30) is right only in A,
    # track 3 is right only in B, track 4 is wrong in both (wrong answer in A).
    a = [_row(1, c, 50), _row(2, c, 30), _row(3, c, 10), _row(4, c, 50, correct=False),
         _row(9, c, 25, label="neg"), _row(8, c, 10, label="neg"),
         _row(1, c, 99, split="val")]  # fmt: skip
    b = [_row(1, c, 50), _row(2, c, 30), _row(3, c, 45), _row(4, c, 10),
         _row(9, c, 25, label="neg"), _row(8, c, 10, label="neg")]  # fmt: skip
    _run(tmp_path, "_a", a, 20)
    _run(tmp_path, "_b", b, 40)
    r = b2_compare.pair("_a", "_b", 0)
    cell = r["accuracy"][c]
    assert cell["n"] == 4
    assert (cell["only_a"], cell["only_b"]) == (1, 1)
    assert cell["accuracy_a"] == pytest.approx(0.5)
    assert cell["accuracy_b"] == pytest.approx(0.5)
    assert r["test_far"] == {"a": 0.5, "b": 0.0}
    assert r["means"]["a"]["real"] == pytest.approx(50.0)


def test_grid_rule_disqualifies_a_setting_that_costs_clean_or_tempo(tmp_path, monkeypatch):
    monkeypatch.setattr(b2_compare, "RES", tmp_path)

    def conds(real, clean, tempo):
        return {"demand_tcar_snr0_10s": {"accuracy": real}, "clean_10s": {"accuracy": clean},
                "speed_+8pct_10s": {"accuracy": tempo},
                "white_snr0_10s": {"accuracy": 0.0}}  # fmt: skip

    _run(tmp_path, "_base", [], 0, conds(0.70, 1.0, 1.0))
    _run(tmp_path, "_ok", [], 0, conds(0.80, 1.0, 0.992))  # 0.4 points below
    _run(tmp_path, "_bad", [], 0, conds(0.90, 1.0, 0.98))  # 1.0 point below
    rows = b2_compare.grid(["_base", "_ok", "_bad"], 0, 0.5)
    assert [r["qualifies"] for r in rows] == [True, True, False]
    assert rows[1]["real"] == pytest.approx(80.0)
    assert rows[1]["kept"] == pytest.approx(99.6)
