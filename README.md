# Landmark

[![ci](https://github.com/anytroops/landmark/actions/workflows/ci.yml/badge.svg)](https://github.com/anytroops/landmark/actions/workflows/ci.yml)

A distributed audio fingerprinting and identification service in Go, in the style of Shazam and YouTube Content ID, that can also tracklist DJ mixes: name every song in a mix and when it plays, even when the DJ sped it up or slowed it down.

- **Identify:** landmark (constellation) hashing, a document-partitioned index of mmap'd immutable segments, and a gRPC coordinator that scatter-gathers each query across replicated shards with deadlines, retries, and replica ejection.
- **Tracklist DJ mixes:** a tempo-invariant triplet fingerprint (hashes built from time and pitch ratios that a tempo change cannot move), a matcher that fits each match's speed and offset, and sequence decoding into a tracklist.

Every component has a Python reference and a Go implementation tested for parity against it (bit-exact for the triplet matcher). Every number below traces to a committed run.

## Status

| Part | State |
|---|---|
| Fingerprinting, index, shard, coordinator, failover, replication | Done; Go matches the Python reference on every test query |
| Benchmarks (latency vs load, max throughput under an SLO, replica kill, index size) | Done on fma_medium (23,985 tracks), single host |
| Stop list for common hashes (D30) | Done, on in the deploy config |
| DJ tracklisting | Working end to end (`cmd/tracklist`); building the hybrid matcher (D36) |

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

  cmd/tracklist: mix -> 10 s windows -> triplet fingerprints -> coordinator -> decoder -> tracklist
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

DJ tracklisting:

| | |
|---|---|
| Triplet fingerprint, 10 s excerpt, one search, turntable or keylock -8% to +8% | 99.8-99.9% (pair fingerprint: 14% at +/-2%, 0% beyond) |
| Synthetic DJ mixes, songs found (clean / with EQ, noise, MP3) | 99.4% / 98.2-98.8%, one search per window |
| UnmixDB (real club tracks with effects, built by other researchers), songs found | speed grid (37 searches per window) 95.2%; triplets 89.6%; hybrid in progress |

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

For DJ mixes, build a triplet index (`bin/ingest -fingerprint triplets ...`), run a coordinator over it, and:

```bash
bin/tracklist -coordinator 127.0.0.1:7500 -index data/segments/medium_b2 mix.mp3
```

Commands: `ingest` (decode, fingerprint, partition, write segments and the stop list), `shard`, `coordinator`, `identify`, `tracklist`, `loadgen` (open-loop load generator), `stoplist`. Python reference and evaluation harness: `prototype/`, `eval/`; benchmark harness: `bench/`.

## Data

Audio comes from [FMA](https://github.com/mdeff/fma) and [UnmixDB](https://zenodo.org/records/1422385) (CC BY-NC-ND 4.0) and lives in the gitignored `data/` directory. The repo stores fingerprints, metadata, and results only, never audio.

## License

MIT
