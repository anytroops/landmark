# Design: live stream monitoring

Status: draft, 2026-10-21. Decisions marked **DECIDE** need Sean's pick.

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

**DECIDE: pick one.**

| | (a) Fixed-lag smoothing | (b) Fixed-lag Viterbi | (c) Enter/exit thresholds |
|---|---|---|---|
| Idea | Run the calibrated smoothing decoder (D32 i) on a sliding buffer; a window's label is final once k windows after it are in | Viterbi over a sliding buffer; commit the best path's label for the window k steps back | A track "enters" after N consecutive accepted windows and "exits" after M windows without it |
| Delay | window + k x hop (10 + 2.5k s) | same form | window + (N-1) x hop |
| Matches the evaluated system | Yes for k at least the smoothing radius: same labels as the offline decoder, so the UnmixDB numbers carry over and can be checked exactly | Approximately (offline Viterbi can revise far back) | No: new parameters, new evaluation |
| Effort | Small | Medium | Small |

Recommendation: (a). With k = 1 (the calibrated radius), labels are final 12.5 s after a window starts, and the output equals offline decoding, which we can test mix by mix.

## L2. Assigning streams to workers

**DECIDE: pick one.**

| | (a) Static config | (b) Leases in a small registry | (c) Consistent hashing over live workers |
|---|---|---|---|
| Idea | Each worker is told which streams to monitor | Workers claim streams from a registry and renew a lease by heartbeat; when a worker dies its leases expire and another worker claims them | Workers announce themselves; each stream goes to the worker its hash maps to; membership changes move few streams |
| Failover | Manual | Automatic, within the lease time | Automatic, needs a membership protocol |
| Distributed-systems content | None | Leases, heartbeats, fencing (a worker that lost its lease must stop reporting) | Membership, rebalancing |
| Effort | Small | Medium | Medium to large |

Recommendation: (b). A registry service (one process, its state in a file; replicating the registry itself is out of scope and stated as such) with lease epochs, so a stale worker's events are rejected (fencing).

## L3. Event delivery

**DECIDE: pick one.**

| | (a) Append-only log per stream | (b) Log plus live subscription |
|---|---|---|
| Idea | Workers append events (track, start, tempo, lease epoch, sequence number) to a log; readers tail it | Same log, plus a gRPC stream that pushes events to subscribers (the demo UI) |
| Duplicates after failover | Detectable: (stream, sequence) is unique; a new lease holder resumes from the last logged sequence | Same |

Recommendation: (b): the log is the source of truth, the subscription feeds the UI.

## Test streams and metrics

Streams are the UnmixDB test mixes and the synthetic test mixes played in real time (or faster, for load tests) by a local stream server, so every event has ground truth.

- Accuracy: the tracklisting metrics, which must equal offline decoding under L1 (a).
- Detection delay: time from a song's true start (middle of the crossfade in) to its event.
- Capacity: concurrent real-time streams per machine before delay or completeness degrades (the hybrid and the 2-search hybrid).
- Failover: kill a worker mid-song; time until its streams resume, events lost or duplicated (by sequence number).

## Limitations to state

- One host: capacity numbers measure one machine.
- The registry is a single process.
- Catalog songs only: commercial radio would need a commercial catalog.
