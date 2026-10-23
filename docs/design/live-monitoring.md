# Design: live stream monitoring

Status: decided 2026-10-21 (D38 to D40: L1 (a), L2 (b), L3 (b)); being built.

Roadmap (Sean's picks, 2026-10-21): (1) live stream monitoring, (2) head-to-head against open-source systems (audfprint, Panako), (3) adding songs while serving, (4) DJ analysis features. This document covers (1).

## Goal

Continuously tracklist many live audio streams (internet radio, DJ livestreams) as they play: emit "song X started at T (tempo +2%)" within seconds of the song starting, for many streams at once, on the existing cluster. This is what broadcast-monitoring services sell (royalty reporting, DJ set logging); a phone app like Shazam identifies one clip on request and does none of it.

Today `cmd/tracklist` needs the whole mix up front and decodes it in one pass.

## What is new

- **Ingest:** read audio from a live source (HTTP stream, file played in real time) and cut it into windows as it arrives.
- **Online decoding:** decide "song X is playing" from the windows seen so far, with a bounded delay, without revising what was already reported (or with an explicit correction).
- **Running many streams:** assign streams to worker processes, move a stream when its worker dies, and keep reported events neither lost nor duplicated beyond what the design allows.
- **Load:** every stream sends about 2.4 hybrid searches per second at real time (one window every 2.5 s, 6 searches each; the 2-search hybrid: 0.8). The cluster's capacity (BENCHMARKS B8) sets how many streams it can carry.

## L1. Online decoder

**Decided: (a), D38.**

| | (a) Fixed-lag smoothing | (b) Fixed-lag Viterbi | (c) Enter/exit thresholds |
|---|---|---|---|
| Idea | Run the calibrated smoothing decoder (D32 i) on a sliding buffer; a window's label is final once k windows after it are in | Viterbi over a sliding buffer; commit the best path's label for the window k steps back | A track "enters" after N consecutive accepted windows and "exits" after M windows without it |
| Delay | window + k x hop (10 + 2.5k s) | same form | window + (N-1) x hop |
| Matches the evaluated system | Yes for k at least the smoothing radius: same labels as the offline decoder, so the UnmixDB numbers carry over and can be checked exactly | Approximately (offline Viterbi can revise far back) | No: new parameters, new evaluation |
| Effort | Small | Medium | Small |

Recommendation: (a). With k = 1 (the calibrated radius), labels are final 12.5 s after a window starts, and the output equals offline decoding, which we can test mix by mix.

## L2. Assigning streams to workers

**Decided: (b), D39.**

| | (a) Static config | (b) Leases in a small registry | (c) Consistent hashing over live workers |
|---|---|---|---|
| Idea | Each worker is told which streams to monitor | Workers claim streams from a registry and renew a lease by heartbeat; when a worker dies its leases expire and another worker claims them | Workers announce themselves; each stream goes to the worker its hash maps to; membership changes move few streams |
| Failover | Manual | Automatic, within the lease time | Automatic, needs a membership protocol |
| Distributed-systems content | None | Leases, heartbeats, fencing (a worker that lost its lease must stop reporting) | Membership, rebalancing |
| Effort | Small | Medium | Medium to large |

Recommendation: (b). A registry service (one process, its state in a file; replicating the registry itself is out of scope and stated as such) with lease epochs, so a stale worker's events are rejected (fencing).

## L3. Event delivery

**Decided: (b), D40.**

| | (a) Append-only log per stream | (b) Log plus live subscription |
|---|---|---|
| Idea | Workers append events (track, start, tempo, lease epoch, sequence number) to a log; readers tail it | Same log, plus a gRPC stream that pushes events to subscribers (the demo UI) |
| Duplicates after failover | Detectable: (stream, sequence) is unique; a new lease holder resumes from the last logged sequence | Same |

Recommendation: (b): the log is the source of truth, the subscription feeds the UI.

## Design as built

Processes:

- `cmd/registry`: one process. Holds the stream list, leases, and epochs (state file `registry.json`, rewritten atomically), and each stream's event log (`events/<stream>.jsonl`). Serves `RegistryService` (gRPC).
- `cmd/monitor`: a worker. Heartbeats to the registry, and for each stream it holds runs: source, windows, hybrid searches (the same code as `cmd/tracklist`), the online decoder, appends.
- The two search clusters (triplet and pair index) as for `cmd/tracklist`.
- `cmd/streamserver` (test only): plays mixes as live HTTP streams on a wall-clock schedule, so every song has a known broadcast time.

Leases (D39). A worker calls `Heartbeat(worker, capacity, held leases)` every 2 s. The registry renews the leases the worker still holds at their current epoch, frees leases not renewed within the TTL (6 s), and grants free streams (by id) up to the worker's capacity, each grant raising the stream's epoch by one and storing it before replying. The reply lists every lease the worker now holds; the worker stops any stream not in it, and stops all streams if no heartbeat succeeds within TTL minus one heartbeat of its last success. A grant carries the next sequence number and the stream's open entry (a start without an end), so the new holder continues the log.

Events (D40). Two kinds, times in Unix seconds of broadcast audio:

- `start`: track, start time, tempo so far. Sent when the first window of a new entry is final (D38).
- `end`: track, start, end, tempo, evidence: the finished entry, equal to the offline tracklist's entry. Sent when the entry can no longer grow or merge: another track's entry has started, or the final windows have passed its end plus the merge gap.

`Append(stream, epoch, events)` is accepted only at the stream's current epoch and only if the events continue the sequence; an exact re-send of logged events is acknowledged without writing. A worker whose append is refused for its epoch stops that stream. `Subscribe(streams)` sends the logged events, then live ones.

After failover. The new holder starts from the live position (the gap's audio is lost). If the log has an open entry, the new holder takes it as the previous entry: if the same track's entry begins within the merge gap of the new holder's first audio, it continues it (no second start; the end keeps the original start, flagged `resumed`); otherwise the new holder closes it with an `end` flagged `interrupted`, ending at its first audio (the song ended somewhere in the gap).

Sources that drop (D45). A file source ends when it is read to the end. A live (http) source never ends by itself: when its connection drops (end of data or a read error), the holder joins it again at once, then with backoff from 0.5 s to 10 s while that fails or while connections end before their first window, keeping its lease. The song in progress continues as after a failover, from the new connection's first audio (the gap is lost). The stream ends only when the server answers 410 Gone (the broadcast is over); the last decoder then closes its last song at its last audio, so a station that finishes its playlist logs exactly what it did before rejoining existed.

Stream time. A source's first sample gets a broadcast time: the stream server's `X-Landmark-Start` header when present, otherwise the time the first bytes arrived. Window starts are seconds since then, rounded to ms as offline.

## Test streams and metrics

Streams are the UnmixDB test mixes played in real time by `cmd/streamserver`, several back to back per station, so every event has ground truth. Harness: `bench/scripts/live.py` (`equiv`: every mix as a file stream against `cmd/tracklist`; `realtime`: stations, workers, an optional `kill -9`), scored by `eval/live_score.py`.

- Accuracy: the tracklisting metrics, which must equal offline decoding under L1 (a).
- Detection delay: time from a song's true start (middle of the crossfade in) to its event.
- Capacity: concurrent real-time streams per machine before delay or completeness degrades (the hybrid and the 2-search hybrid).
- Failover: kill a worker mid-song; time until its streams resume, events lost or duplicated (by sequence number).

## Limitations to state

- One host: capacity numbers measure one machine.
- The registry is a single process.
- Catalog songs only: commercial radio would need a commercial catalog.
