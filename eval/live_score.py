"""Score a live monitoring run (D38-D40) against the broadcast ground truth.

  python -m eval.live_score --run eval/results/live/<run>
  python -m eval.live_score --equiv eval/results/live/equiv_set044   # stream-time delay

A run directory (from bench.scripts.live realtime) holds schedule.jsonl
(cmd/streamserver: which mix each station played, and when), events/ (the
registry's logs), and run.json (the mixes file, monitored interval, kills).
Each station's songs become segments in broadcast time (the mix's labels
shifted by the mix's start); a segment counts if it lies inside the
monitored interval. Entries are the logged end events, plus start events
still open when the run stopped (ending then). Matching is as in
eval.tracklist_score.score_mix: same duplicate cluster, overlapping in time.

Reported, besides found / known and precision:
- detection delay: from the true transition (middle of the crossfade into
  the song, its start for a mix's first song) to the worker deciding the
  song's start event;
- repeats: a second entry for a segment already found (after a failover,
  a song both holders reported);
- per failover: when the new holder heard its first audio, its first
  event, and the songs that played in the gap.
"""

import argparse
import json
from collections import defaultdict
from pathlib import Path

import numpy as np

from eval.tracklist import cluster_table

ROOT = Path(__file__).resolve().parent.parent


def load_events(run: Path) -> dict[str, list[dict]]:
    out = {}
    for log in sorted((run / "events").glob("*.jsonl")):
        out[log.stem] = [json.loads(x) for x in log.read_text().splitlines()]
    return out


def truth(run: Path, mixes: dict) -> dict[str, list[dict]]:
    """Each station's segments in broadcast time."""
    by_file = {m["file"]: m for m in mixes["mixes"]}
    out = defaultdict(list)
    for line in (run / "schedule.jsonl").read_text().splitlines():
        s = json.loads(line)
        file = str(Path(s["file"]).resolve().relative_to(ROOT))
        for seg in by_file[file]["segments"]:
            t = s["start_unix"]
            out[s["station"]].append({
                "dense": seg["dense"], "known": seg["known"], "ignore": seg.get("ignore", False),
                "start": t + seg["mix_start"], "end": t + seg["mix_end"],
                "transition": t + seg["mix_start"] + seg["fade_in"] / 2,
                "file": file, "file_end": s["end_unix"],
            })  # fmt: skip
    return out


def entries(events: list[dict], stop: float) -> list[dict]:
    """End events, plus a start still open at stop; each with its start
    event's decision time."""
    out, open_start = [], None
    starts = {}
    for e in events:
        key = (e.get("trackId", 0), e["startUnix"])
        if e["kind"] == "EVENT_KIND_START":
            starts[key] = e
            open_start = e
        else:
            s = starts.get(key)
            out.append({"track": key[0], "start": e["startUnix"], "end": e["endUnix"],
                        "tempo": e["tempo"], "decided": s["decidedUnix"] if s else None,
                        "interrupted": e.get("interrupted", False),
                        "resumed": e.get("resumed", False), "epoch": int(e["epoch"])})  # fmt: skip
            open_start = None
    if open_start is not None:
        e = open_start
        out.append({"track": e.get("trackId", 0), "start": e["startUnix"], "end": stop,
                    "tempo": e["tempo"], "decided": e["decidedUnix"], "interrupted": False,
                    "resumed": False, "epoch": int(e["epoch"])})  # fmt: skip
    return out


def score_station(ents: list[dict], segs: list[dict], cluster: np.ndarray,
                  begin: float, stop: float) -> dict:  # fmt: skip
    """Segments inside [begin, stop] count; an entry naming a segment outside
    it is neither correct nor wrong (the boundary cut it)."""
    known = [s for s in segs if s["known"]]
    inside = [s["start"] >= begin and s["end"] <= stop and not s["ignore"] for s in known]
    found, delays = set(), []
    correct = repeats = boundary = 0
    for e in ents:
        hits = [i for i, s in enumerate(known)
                if e["track"] >= 0 and cluster[e["track"]] == cluster[s["dense"]]
                and e["start"] < s["end"] and e["end"] > s["start"]]  # fmt: skip
        if not hits:
            continue
        i = hits[0]
        if not inside[i]:
            boundary += 1
            continue
        correct += 1
        if i in found:
            repeats += 1
            continue
        found.add(i)
        if e["decided"] is not None:
            delays.append(e["decided"] - known[i]["transition"])
    scored = sum(1 for x in inside if x)
    return {"entries": len(ents) - boundary, "correct": correct, "known": scored,
            "found": len(found), "repeats": repeats, "delays": delays}  # fmt: skip


def failovers(events: list[dict], segs: list[dict], kill: float | None) -> list[dict]:
    """Epoch changes in one stream's log."""
    out = []
    prev = None
    for e in events:
        ep = int(e["epoch"])
        if prev is not None and ep != prev["epoch"]:
            first_audio = e["sourceStartUnix"]
            gap_from = kill if kill is not None else prev["last_logged"]
            out.append({
                "epoch": ep, "first_audio_unix": first_audio, "first_event_unix": e["loggedUnix"],
                "audio_lost_s": first_audio - gap_from if kill is not None else None,
                "first_event_after_kill_s": e["loggedUnix"] - kill if kill is not None else None,
                "songs_starting_in_gap": sum(1 for s in segs if s["known"]
                                             and gap_from <= s["transition"] < first_audio),
            })  # fmt: skip
        prev = {"epoch": ep, "last_logged": e["loggedUnix"]}
    return out


def score(run: Path) -> dict:
    meta = json.loads((run / "run.json").read_text())
    mixes = json.loads((ROOT / "eval" / "mixes" / f"{meta['mixes']}.json").read_text())
    cluster = cluster_table("", str(ROOT / meta["catalog"]))
    segs = truth(run, mixes)
    events = load_events(run)
    tot = defaultdict(int)
    delays, fails = [], []
    for station, ss in sorted(segs.items()):
        ev = events.get(station, [])
        begin = min((e["sourceStartUnix"] for e in ev), default=meta["stop_unix"])
        r = score_station(entries(ev, meta["stop_unix"]), ss, cluster, begin, meta["stop_unix"])
        for k in ("entries", "correct", "known", "found", "repeats"):
            tot[k] += r[k]
        delays += r["delays"]
        fails += [{"station": station, **f} for f in failovers(ev, ss, meta.get("kill_unix"))]
        seqs = [int(e["seq"]) for e in ev]
        tot["sequence_gaps"] += seqs != list(range(1, len(seqs) + 1))
    d = np.array(delays)
    out = {
        **tot,
        "found_pct": round(100 * tot["found"] / max(tot["known"], 1), 2),
        "precision_pct": round(100 * tot["correct"] / max(tot["entries"], 1), 2),
        "delay_s": {
            q: round(float(np.percentile(d, p)), 2)
            for q, p in (("p10", 10), ("p50", 50), ("p90", 90), ("max", 100))
        }
        if len(d)
        else {},  # fmt: skip
        "failovers": fails,
    }
    return out


def stream_delays(run: Path, mixes_name: str = "unmixdb_s8", window: float = 10, hop: float = 2.5,
                  k: int = 1) -> dict:  # fmt: skip
    """Detection delay in stream time from an equiv run (live_runs.jsonl):
    a song's start event is decided once its entry's first window and k
    more are in, at entry start + window + k * hop, measured from the true
    transition. Search time comes on top in real time."""
    gt = json.loads((ROOT / "eval" / "mixes" / f"{mixes_name}.json").read_text())
    by_file = {m["file"]: m for m in gt["mixes"]}
    meta = json.loads((ROOT / "data/segments/medium_unmixdb_b2/catalog.json").read_text())
    cluster = np.array([t["cluster"] for t in meta], dtype=np.int64)
    delays = []
    for line in (run / "live_runs.jsonl").read_text().splitlines():
        r = json.loads(line)
        found = set()
        segs = [s for s in by_file[r["file"]]["segments"] if s["known"] and not s.get("ignore")]
        for e in r["entries"]:
            for i, s in enumerate(segs):
                same = cluster[e["track"]] == cluster[s["dense"]]
                overlap = e["start_s"] < s["mix_end"] and e["end_s"] > s["mix_start"]
                if i not in found and same and overlap:
                    found.add(i)
                    delays.append(
                        e["start_s"] + window + k * hop - s["mix_start"] - s["fade_in"] / 2
                    )
                    break
    d = np.array(delays)
    return {"songs": len(d), "window_s": window, "hop_s": hop, "k": k,
            "delay_s": {q: round(float(np.percentile(d, p)), 2) for q, p in
                        (("p10", 10), ("p50", 50), ("p90", 90), ("max", 100))}}  # fmt: skip


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--run", type=Path)
    ap.add_argument("--equiv", type=Path)
    args = ap.parse_args()
    if args.equiv:
        r = stream_delays(args.equiv)
        (args.equiv / "delay.json").write_text(json.dumps(r, indent=1) + "\n")
        print(json.dumps(r, indent=1))
        return
    r = score(args.run)
    (args.run / "score.json").write_text(json.dumps(r, indent=1) + "\n")
    print(json.dumps({k: v for k, v in r.items() if k != "failovers"}, indent=1))
    for f in r["failovers"]:
        print(f)


if __name__ == "__main__":
    main()
