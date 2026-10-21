import numpy as np

from eval.live_score import entries, failovers, score_station


def ev(seq, kind, track, start, end, epoch=1, decided=None, source=0.0, logged=None, **kw):
    decided = decided if decided is not None else end
    e = {"seq": str(seq), "kind": f"EVENT_KIND_{kind}", "trackId": track, "startUnix": start,
         "endUnix": end, "tempo": 1.0, "epoch": str(epoch), "sourceStartUnix": source,
         "decidedUnix": decided, "loggedUnix": logged or end}  # fmt: skip
    return {**e, **kw}


def seg(dense, start, end, fade_in=0.0):
    return {"dense": dense, "known": True, "ignore": False, "start": start, "end": end,
            "transition": start + fade_in / 2}  # fmt: skip


CLUSTER = np.arange(10)


def test_entries_pair_ends_with_starts_and_keep_an_open_start() -> None:
    events = [ev(1, "START", 3, 100, 110, decided=113), ev(2, "END", 3, 100, 150),
              ev(3, "START", 4, 145, 155, decided=158)]  # fmt: skip
    got = entries(events, stop=200)
    assert [(e["track"], e["start"], e["end"], e["decided"]) for e in got] == [
        (3, 100, 150, 113), (4, 145, 200, 158)]  # fmt: skip


def test_score_station_counts_found_precision_repeats_and_delay() -> None:
    segs = [seg(3, 100, 150), seg(4, 140, 190, fade_in=10), seg(5, 300, 350)]
    ents = [
        {"track": 3, "start": 100, "end": 150, "decided": 112.5},
        {"track": 4, "start": 142.5, "end": 190, "decided": 160},
        {"track": 4, "start": 170, "end": 190, "decided": 182},  # repeat
        {"track": 7, "start": 200, "end": 220, "decided": 212},  # wrong
        {"track": 5, "start": 300, "end": 350, "decided": 312},  # outside the run: boundary
    ]
    r = score_station(ents, segs, CLUSTER, begin=100, stop=250)
    assert (r["known"], r["found"], r["entries"], r["correct"], r["repeats"]) == (2, 2, 4, 3, 1)
    assert r["delays"] == [12.5, 15.0]  # transition of track 4 is 145


def test_failovers_measure_the_gap() -> None:
    events = [ev(1, "START", 3, 100, 110, epoch=1, logged=113),
              ev(2, "END", 3, 100, 160, epoch=2, source=128, logged=140,
                 interrupted=True)]  # fmt: skip
    segs = [seg(3, 100, 150), seg(4, 122, 170)]
    (f,) = failovers(events, segs, kill=120)
    assert f["epoch"] == 2 and f["audio_lost_s"] == 8 and f["first_event_after_kill_s"] == 20
    assert f["songs_starting_in_gap"] == 1
