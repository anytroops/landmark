import numpy as np

from eval.peak_survival import survival
from prototype import b2


def _peaks(tf):
    t, f = (np.array(x, dtype=np.int64) for x in zip(*tf, strict=True))
    return b2.Peaks(t, f, np.zeros(len(t)))


def test_survival_counts_peaks_within_one_frame_and_one_band():
    ref = {(10, 20), (50, 100)}
    q = _peaks([(11, 21), (50, 102), (9, 19), (200, 5)])
    assert survival(q, ref) == 0.5


def test_survival_of_no_peaks_is_zero():
    assert survival(_peaks([(0, 0)]), set()) == 0.0
    empty = b2.Peaks(np.empty(0, np.int64), np.empty(0, np.int64), np.empty(0))
    assert survival(empty, {(1, 1)}) == 0.0
