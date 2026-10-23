# Landmark

[![ci](https://github.com/anytroops/landmark/actions/workflows/ci.yml/badge.svg)](https://github.com/anytroops/landmark/actions/workflows/ci.yml)

A distributed audio fingerprinting and identification service in Go, in the style of Shazam and YouTube Content ID, that can also tracklist DJ mixes: name every song in a mix and when it plays, even when the DJ sped it up or slowed it down.

- **Identify:** landmark (constellation) hashing, a document-partitioned index of mmap'd immutable segments, and a gRPC coordinator that scatter-gathers each query across replicated shards with deadlines, retries, and replica ejection.
- **Tracklist DJ mixes:** a tempo-invariant triplet fingerprint (hashes built from time and pitch ratios that a tempo change cannot move), a matcher that fits each match's speed and offset, and sequence decoding into a tracklist.
- **Add songs while serving:** new songs go into small immutable delta segments published as a new index version (a manifest); replicas swap versions without a restart, and the coordinator pins every query to one version so an answer never mixes catalogs.
- **Monitor live streams:** workers lease streams from a registry by heartbeat and report songs as they play; leases carry epochs (fencing tokens), so a worker that lost its stream cannot log next to its replacement; a stream whose connection drops is joined again and its song in progress continues; and the online decoder reports exactly what offline tracklisting would.

Every component has a Python reference and a Go implementation tested for parity against it (bit-exact for the triplet matcher). Every number below traces to a committed run.

## Status

| Part | State |
|---|---|
| Fingerprinting, index, shard, coordinator, failover, replication | Done; Go matches the Python reference on every test query |
| Benchmarks (latency vs load, max throughput under an SLO, replica kill, index size) | Done on fma_medium (23,985 tracks), single host |
| Stop list for common hashes (D30) | Done, on in the deploy config |
| DJ tracklisting | Working end to end (`cmd/tracklist`): hybrid matcher (D36), pruned triplet search (D37), accuracy and cost measured |
| Adding songs while serving (D42, D47) | Done: delta segments, versioned manifests, shards that swap versions under load, version-pinned queries, `ingest -add` and `-merge`; measured under load. Size-tiered merging (D47) keeps a few deltas and rewrites the base once per 10% of growth |
| Head-to-head against audfprint and Panako (D41) | Done: same tracks, queries, and accept rule, each tool's density tuned on val; accuracy, DJ mixes, and cost |
| DJ mix analysis (D43) | Done: `tracklist -analyze` reports each song's fades, turntable or keylock, and BPM in the set; the Go port matches the Python prototype on all 1,058 test songs |
| Demo web page (D46) | Done: `make demo`. Identify a clip (a file, the microphone, or a test song sped up like a turntable or with keylock, pair and triplet answers side by side), tracklist a mix while its windows are searched (fades, turntable or keylock, BPM, the true tracklist beside it), watch live stations |
| Live stream monitoring (D38-D40) | Done: registry (leases, epochs, event logs, subscriptions), workers, online decoder; live output equals offline on 96 test mixes; real-time capacity and worker failover measured |

## Architecture

```
  audio --> cmd/ingest --> segments (one per partition, mmap'd, CRC-checked)
                               |
  client                       v
    | gRPC Identify   +--> shard P0 (replicas a, b)
    v                 |
  coordinator --------+--> shard P1 (replicas a, b)    each shard: worker pool,
  stop list, deadline,|                                load shedding, offset voting
  failover, merge     +--> ...                         (pairs) or line fits (triplets)

  cmd/tracklist: mix -> 10 s windows -> triplet + pair searches (two clusters) -> decoder -> tracklist

  cmd/registry: streams, leases + epochs, per-stream event logs, live subscriptions
       ^ heartbeat, append (fenced by epoch)
  cmd/monitor (workers): live audio -> windows -> hybrid searches -> online decoder -> events
```

Design decisions, with the options considered for each: [docs/DECISIONS.md](docs/DECISIONS.md).

## Results

Accuracy: [docs/EVAL.md](docs/EVAL.md). Performance: [docs/BENCHMARKS.md](docs/BENCHMARKS.md). One laptop (Apple M4 Pro, 12 cores), so partition scaling there measures parallelism, not distribution.

| | |
|---|---|
| Catalog | 23,985 fma_medium tracks |
| Accuracy, 10 s clean excerpt | 99.9% (false accepts 0.1%) |
| Accuracy, 10 s at 0 dB SNR white noise | 97.1% |
| Max throughput under p99 <= 100 ms, one partition | 550 qps; 719 qps with the stop list |
| Latency at about 250 qps (8 partitions vs 1) | p50 3.4 vs 14.6 ms, p99 7.4 vs 36.3 ms |
| Replica killed under load | 100% complete answers; p99 back to about 9 ms the next second |
| Radix vote sort + packed requests (D27, D28) | p99 3.0x lower, capacity 2.3x |

Against open-source systems (same tracks, queries, and accept rule; each tool's main setting tuned on val):

| | Landmark | audfprint | Panako |
|---|---|---|---|
| 10 s clip, white noise at 0 dB SNR | 98.1% (pairs) | 54.0% | 52.0% |
| 10 s clip, turntable or keylock -8% to +8% | 99.7-99.9% (triplets) | 0-99.8% | 72.5-89.6% |
| Songs found in UnmixDB DJ mixes | 95.2% | 80.1% | 78.6% |
| Query time per clip, one core | 75 ms (pairs), 179 ms (triplets) | 84 ms | 235 ms |

DJ tracklisting:

| | |
|---|---|
| Triplet fingerprint, 10 s excerpt, one search, turntable or keylock -8% to +8% | 99.8-99.9% (pair fingerprint: 14% at +/-2%, 0% beyond) |
| Synthetic DJ mixes, songs found (clean / with EQ, noise, MP3) | 99.4% / 98.2-98.8%, one search per window |
| UnmixDB (real club tracks with effects, built by other researchers), songs found | hybrid triplets + pairs (6 searches per window) **95.2%**, precision 98.9%; speed grid (37 searches) 95.2%, precision 98.8% |
| Live monitoring, UnmixDB test sets played as streams | same entries as offline tracklisting on all 384 mixes, which equal the eval's (95.2% of songs, precision 98.9%); a song is reported a median 12.5 s (stream time) after its transition |
| Live monitoring in real time, one laptop | keeps up with 48 stations (window lag p99 2.3 s against a 2.5 s hop, 94.9% of songs found; 32 before D44 computed each query FFT once for both filterbanks), saturated at 56; a killed worker's 16 streams resume on a standby, 5.2 s of audio lost each |
| Adding songs while serving, under 200 qps | 100 songs searchable about 3 s after the add starts; p99 20.8 ms at baseline, 28.4 ms during adds; no errors |
| Search cost, one partition | triplet search 378 qps under p99 <= 100 ms after pruning (D37; 118 qps before), pair search 734 qps: the hybrid tracklists 3.5-4.2x as many windows per second as the speed grid (10-13x with its 2-search variant) |

## Usage

Requires Go, buf, golangci-lint, uv, ffmpeg, and Docker with compose (for the containerized cluster).

```bash
make test         # go test -race + pytest
make lint         # go vet, golangci-lint, buf lint, ruff
make build        # binaries into bin/
make cluster-up   # 2 partitions x 2 replicas, coordinator, Prometheus :9090, Grafana :3000
```

Build an index, serve it, and query it:

```bash
bin/ingest -manifest eval/manifests/splits_medium_seed0.json -out data/segments/medium/p1 -partitions 1
bin/shard -segment data/segments/medium/p1/part-000.lmrk -listen 127.0.0.1:7001 -admin 127.0.0.1:9001
bin/identify -shard 127.0.0.1:7001 -catalog data/segments/medium/p1/catalog.json -start 10 song.mp3
```

For DJ mixes, build a triplet index (`bin/ingest -fingerprint triplets ...`) beside the pair index, run a coordinator over each (with `stop_fraction` 0.04, which the decoder's thresholds were calibrated with), and:

```bash
bin/tracklist -coordinator 127.0.0.1:7500 -index data/segments/medium_b2 \
    -pairs-coordinator 127.0.0.1:7600 -pairs-index data/segments/medium mix.mp3
```

With `-analyze` it also reports how each song was mixed in (D43): its fade-in and fade-out, whether a tempo change was made like a turntable or with keylock, and its BPM in the set (from `bpm.json` beside the index, written by `python -m eval.dj_bpm --index <dir>`). It needs the songs' reference audio:

```
 2. 0:40-1:12  set044/pntg001-04-demir-you_got_it.excerpt40.mp3   -2.4%  (evidence 4849)  in 0:35-0:40  out 1:07-1:14  turntable  120.1 BPM
```

Live monitoring: run a registry with the streams to watch, and one or more workers against the two clusters (each stream in `streams.json` is `{"id": ..., "url": ...}`; an http(s) URL is read live):

```bash
bin/registry -dir data/monitor -streams streams.json
bin/monitor -registry 127.0.0.1:7700 -capacity 8 \
    -coordinator 127.0.0.1:7500 -index data/segments/medium_b2 \
    -pairs-coordinator 127.0.0.1:7600 -pairs-index data/segments/medium
```

To add songs to a serving index, run its shards with `-index <dir> -partition <p>` (they follow its newest version) and:

```bash
bin/ingest -out data/segments/medium -add new_songs.tsv   # name<TAB>path per line; publishes the next version, then merges size-tiered (D47)
bin/ingest -out data/segments/medium -merge               # fold delta segments into new bases
```

The demo web page on http://127.0.0.1:8080 (both clusters, three looping test stations with a worker, and `cmd/demo`; `STATIONS=0` skips live monitoring):

```bash
make demo
```

To watch live monitoring in the terminal instead (both clusters, a registry, a stream server playing test mixes as stations, a worker, and `events` printing songs as they are recognized):

```bash
python -m bench.scripts.live_demo --stations 3
```

Commands: `ingest` (decode, fingerprint, partition, write segments and the stop list; add songs; merge), `demo` (the web page), `events` (follow live monitoring), `shard`, `coordinator`, `identify`, `tracklist`, `registry` and `monitor` (live monitoring), `streamserver` (test stations with known playlists), `loadgen` (open-loop load generator), `stoplist`. Python reference and evaluation harness: `prototype/`, `eval/`; benchmark harness: `bench/`.

## Data

Audio comes from [FMA](https://github.com/mdeff/fma) and [UnmixDB](https://zenodo.org/records/1422385) (CC BY-NC-ND 4.0) and lives in the gitignored `data/` directory. The repo stores fingerprints, metadata, and results only, never audio.

## License

MIT
