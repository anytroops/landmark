# Design: adding songs while serving

Status: decided 2026-10-22 (D42: W1 (a), W2 (a), W3 (a)); not started. Roadmap item 3.

## Goal

Add songs to a running cluster and have them identifiable within seconds, with no restart and no query failing or slowing past the SLO while they go in. Today the catalog is fixed at ingest: segments are immutable and mmap'd (D3), and changing them means re-ingesting and restarting every shard. A real service adds new releases daily.

## What exists

- Partitioning by track (D2): each partition holds every posting of its tracks; the coordinator asks every partition and merges.
- Immutable segments per partition, mmap'd, identified by their CRC (D3, D26); the coordinator refuses replicas whose segment differs.
- Two replicas per partition, no consensus (D4): safe because replicas never change.
- The stop list (D30) and catalog.json are computed over the whole catalog at ingest.

Live writes break the last two assumptions: replicas change, so they can differ, and global counts drift.

## W1. Where new postings live

**Decided (see the status line).**

| | (a) Delta segments | (b) In-memory delta plus write-ahead log | (c) Rebuild and swap |
|---|---|---|---|
| Idea | Each add batch is fingerprinted and written as a small immutable segment; a shard searches its base segment and its deltas and merges votes; a background merge folds deltas into a new base | Adds go to a mutable in-memory index (and an append-only log for crash recovery); searched with the base; flushed to a segment when large | Build a new full segment offline with the new songs; the shard switches to it atomically (new mmap, swap a pointer, drain old searches, unmap) |
| Visible after | the batch's segment is written (seconds) | the add returns (milliseconds) | the rebuild (minutes at 25k tracks) |
| Keeps D3 (immutable, mmap) | Yes | Mostly (the delta is not) | Yes |
| Search cost | grows with the number of deltas until a merge | one extra lookup per query | none |
| Effort | Medium | Large | Small to medium |

Recommendation: (a). It keeps every segment immutable and verifiable by CRC, makes durability trivial (the segment file is the write), and the merge is the classic LSM step (log-structured merge: many small sorted files merged in the background) with a measurable cost. (c) is the simple baseline and also how a merge publishes its result.

## W2. Keeping replicas consistent

**Decided (see the status line).**

| | (a) Versioned manifests | (b) Primary per partition with log shipping | (c) Consensus (Raft) per partition |
|---|---|---|---|
| Idea | A partition's content is a manifest: an ordered list of segment CRCs with a version number. Replicas load a manifest; the coordinator only merges answers from replicas at the same version, and moves to a new version once a replica of every partition has it | One replica takes writes and ships segments to the other; readers follow the primary's version | Replicas agree on each add through a consensus log |
| Failure handling | A replica that misses a version is behind; it is not used until it catches up | Primary failover needs election (the hard part) | Built in, at the cost of a large component |
| Fits D4 (no consensus) | Yes: writes are whole immutable files, and the version picks which files | Partly | No |
| Effort | Medium | Large | Very large |

Recommendation: (a). Content-addressed immutable files plus a version number is how many real systems avoid consensus on the data path; only "what is the current version" needs agreement, and a single writer (the ingest service) can own it.

## W3. Global state: the stop list and track ids

**Decided (see the status line).**

| | (a) Freeze the stop list; extend ids | (b) Recompute at merges |
|---|---|---|
| Idea | New songs get the next dense ids (catalog.json append); the stop list keeps its ingest-time counts until the next full rebuild | Each merge recomputes hash counts and publishes a new stop list with the manifest version |
| Accuracy risk | Counts drift as the catalog grows; a hash that becomes common is not stopped (slower, not wrong) | None |
| Effort | Small | Medium |

Recommendation: (a) first, with the drift measured (postings scanned per query as songs are added), then (b) if the drift matters.

## Design as built (D42)

- **Ids.** New songs take the next global dense ids (D19: partition = id mod P, local id = id / P), so a partition's new songs continue its local numbering. A delta segment stores local ids like any segment; its header's track count is the partition's id range so far (base plus every earlier delta plus its own), because readers check that postings stay inside it.
- **Search over a set.** A shard serves a segment set: its base and deltas, searched as one source whose postings for a hash are each segment's postings in turn. Votes are sorted before counting, so the order does not matter: a set must answer exactly as one segment holding the same postings, which is tested on the golden queries for both matchers.
- **Manifests.** Each partition directory holds `manifest-<version>.json`: the version, and the segment files with their CRC-32C and track counts, written atomically. A shard loads the newest manifest at start, checks every listed CRC, polls for a newer one, opens it, switches searches to it atomically, and closes the old set once its searches drain. Info reports the manifest version and a checksum over the listed CRCs.
- **Coordinator.** A replica serves one version; the coordinator answers each query from replicas of a single version (the newest one that every partition has a verified replica of), so an answer never mixes catalogs. A replica behind it is skipped until it catches up.
- **Adding.** `ingest add` fingerprints new files, assigns the next ids, writes one delta per partition that received songs, then manifests at version N+1 for every partition, then the extended catalog. Readers ignore a manifest until every file it lists exists with its CRC.
- **Merging.** `ingest merge` folds a partition's base and deltas into a new base and publishes it as the next version; results are identical by the set property above.
- **Stop list (W3).** Frozen at its ingest-time counts; drift measured as postings scanned per query while songs are added.

## How to show it works

- Add 1,000 fma_medium tracks held out of the index in batches while load runs at a fixed rate: p99 latency and errors during adds and merges, time from add to identifiable, accuracy on the new tracks equal to tracks present from the start.
- Kill a replica during an add: answers never mix versions; the replica catches up when it returns.
- Live monitoring picks up a newly added song in a running stream.

## Limitations to state

- One writer (the ingest service); concurrent writers would need coordination.
- Deletes (takedowns) are out of scope: they would be tombstones filtered at search and dropped at merge.
