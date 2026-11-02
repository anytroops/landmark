import pytest

from eval.cue_accuracy import errors, summary

SEG = {"known": True, "dense": 4, "src_start": 10.0, "factor": 1.05,
       "mix_start": 20.0, "mix_end": 60.0}  # fmt: skip


def test_errors_use_the_segments_line_and_skip_other_songs() -> None:
    entries = [
        {"track": 4, "start_s": 30.0, "end_s": 50.0, "source_start_s": 20.6, "source_end_s": 41.5},
        {"track": 5, "start_s": 30.0, "end_s": 50.0, "source_start_s": 1.0, "source_end_s": 2.0},
        {"track": 4, "start_s": 30.0, "end_s": 50.0},  # no positions
        {"track": 4, "start_s": 70.0, "end_s": 80.0, "source_start_s": 0.0, "source_end_s": 0.0},
    ]
    got = errors(entries, [SEG, {**SEG, "dense": 5, "known": False}])
    # Truth at 30 s: 10 + 1.05 * 10 = 20.5; at 50 s: 10 + 1.05 * 30 = 41.5.
    assert len(got) == 1
    assert got[0] == pytest.approx((0.1, 0.0))


def test_summary() -> None:
    s = summary([0.01, 0.02, 0.3, 2.0])
    assert s["n"] == 4
    assert s["within_0.05_s"] == 0.5
    assert s["within_0.5_s"] == 0.75
    assert s["max_s"] == 2.0
    assert summary([]) == {"n": 0}
