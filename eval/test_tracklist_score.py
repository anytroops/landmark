import numpy as np
import pandas as pd

from eval.tracklist_score import (
    FRAMES_PER_S,
    Entry,
    decode_align,
    decode_smooth,
    fit_line,
    runs,
    score_mix,
)


def test_runs_merges_consecutive_labels() -> None:
    labels = [-1, 3, 3, -1, 3, 5, 5]
    t0s = [0.0, 2.5, 5.0, 7.5, 10.0, 12.5, 15.0]
    assert runs(labels, t0s, 10.0) == [(3, 2.5, 15.0), (3, 10.0, 20.0), (5, 12.5, 25.0)]


def test_fit_line_finds_slope_and_ignores_outliers() -> None:
    t = np.arange(0, 50, 2.5)
    r = 7.0 + 1.04 * t
    r[[3, 9]] += [40.0, -25.0]  # two chance matches off the line
    w = np.full(len(t), 100.0)
    s, a, inl = fit_line(t, r, w, tol=0.3, slopes=np.round(np.arange(0.9, 1.1001, 0.0025), 4))
    assert abs(s - 1.04) < 1e-6 and abs(a - 7.0) < 1e-4
    assert inl.sum() == len(t) - 2 and not inl[3] and not inl[9]


def row(t0: float, factor: float, track: int, score: int, runner: int, offset: int) -> dict:
    return {"mix": 0, "t0": t0, "mode": "speed", "factor": factor, "top1_dense": track,
            "score": score, "runner_up": runner, "offset": offset}  # fmt: skip


def windows_for(
    plays: list[tuple[int, float, float, float, float]], noise: int = 0
) -> pd.DataFrame:
    """Synthetic window rows: plays are (track, start, end, ref_start, factor);
    every window inside a play matches it with a high score at the right
    offset; `noise` extra rows per window are chance matches."""
    rng = np.random.default_rng(0)
    rows = []
    for t0 in np.arange(0, 100, 2.5):
        for track, a, b, ref0, f in plays:
            if a <= t0 and t0 + 10 <= b:
                ref = ref0 + (t0 - a) * f
                off = int(round(ref * FRAMES_PER_S))
                rows.append(row(t0, f, track, 300, 5, off))
        for _ in range(noise):
            track, sc, off = rng.integers(100, 200), rng.integers(5, 15), rng.integers(0, 2000)
            rows.append(row(t0, 1.0, int(track), int(sc), 4, int(off)))
    return pd.DataFrame(rows)


PLAYS = [(1, 0.0, 52.0, 3.0, 1.03), (2, 45.0, 100.0, 1.0, 0.96)]


def test_decode_align_recovers_tracks_speeds_and_boundaries() -> None:
    entries = decode_align(windows_for(PLAYS, noise=3), 10.0, min_evidence=200, switch=4)
    assert [e.track for e in entries] == [1, 2]
    assert abs(entries[0].factor - 1.03) < 1e-3 and abs(entries[1].factor - 0.96) < 1e-3
    assert entries[0].start == 0.0 and 40.0 <= entries[1].start <= 47.5


def test_decode_smooth_rejects_low_scores() -> None:
    entries = decode_smooth(windows_for(PLAYS, noise=3), 10.0, t_abs=50, t_ratio=2, k=1)
    assert [e.track for e in entries] == [1, 2]


def test_score_mix_counts_duplicates_and_unknowns() -> None:
    cluster = np.arange(10)
    cluster[7] = 1  # track 7 is a duplicate of track 1
    segments = [
        {"dense": 1, "known": True, "mix_start": 0.0, "mix_end": 50.0, "fade_in": 0.0, "pct": 3.0},
        {
            "dense": -1,
            "known": False,
            "mix_start": 45.0,
            "mix_end": 80.0,
            "fade_in": 5.0,
            "pct": 0.0,
        },
        {
            "dense": 2,
            "known": True,
            "mix_start": 75.0,
            "mix_end": 100.0,
            "fade_in": 6.0,
            "pct": -4.0,
        },
    ]
    entries = [Entry(7, 0.0, 47.5, 1.03, 1.0), Entry(4, 50.0, 70.0, 1.0, 1.0)]
    r = score_mix(entries, segments, cluster)
    assert (r["entries"], r["correct"], r["known"], r["found"]) == (2, 1, 2, 1)
    assert r["start_err"] == [0.0] and abs(r["speed_err"][0]) < 1e-9


def test_merge_repeats_joins_one_play_split_by_unknown() -> None:
    from eval.tracklist_score import Entry, merge_repeats

    entries = [Entry(1, 0, 42, 1.0, 900), Entry(2, 40, 65, 0.977, 400),
               Entry(2, 60, 70, 0.977, 400), Entry(2, 72, 80, 0.98, 500),
               Entry(3, 70, 107, 1.016, 800), Entry(2, 100, 110, 0.977, 50)]  # fmt: skip
    got = merge_repeats(entries, 2.5)
    assert [(e.track, e.start, e.end, e.factor, e.evidence) for e in got] == [
        (1, 0, 42, 1.0, 900), (2, 40, 80, 0.98, 500), (3, 70, 107, 1.016, 800),
        (2, 100, 110, 0.977, 50)]  # fmt: skip
    assert entries[1].end == 65  # inputs untouched
