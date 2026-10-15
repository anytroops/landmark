# Benchmarks

Methodology: see `docs/PROJECT_BRIEF.md` section 14. Every number here traces to a file in `bench/results/`.

| ID | Benchmark | Result |
|---|---|---|
| B1 | Latency p50/p95/p99/p99.9 vs offered QPS, per partition count | quick sweeps on fma_small (below); full methodology on fma_medium [TBD] |
| B2 | Max sustainable QPS under p99 SLO vs partitions | [TBD] |
| B3 | Failure-injection timeline around a replica kill | [TBD] |
| B4 | Index size: raw vs encoded, bytes per posting | fma_small done (below); fma_large [TBD] |
| B5 | Ingest throughput vs worker count | fma_small, AC, 3 reps (below) |

## Fingerprint throughput, Go vs Python (Phase 2)

Same 200 fma_small tracks (5,997 s of audio), decoded before timing, so only fingerprinting is measured. All-cores runs split tracks into equal static chunks; wall time is the slowest worker. 3 reps each. Apple M4 Pro, 12 cores (8 performance + 4 efficiency), macOS. Raw: `bench/results/fp_throughput_go.json`, `bench/results/fp_throughput_python.json` (commit cebf72b, clean).

| Implementation | 1 core (x realtime) | 12 workers (x realtime) |
|---|---|---|
| Go (gonum FFT, vHGW max filter) | 1,221-1,222 | 9,290-9,722 |
| Python (numpy pocketfft, scipy max filter) | 1,712-1,720 | 7,293-9,943 |

- Single core, Go is ~0.71x numpy's speed. numpy/scipy run the FFT and max filter in optimized C; the Go code is plain loops. Before D18, Go was ~5x slower; pprof traced it to the max filter.
- 12-worker Python runs last ~0.6-0.8 s, so they are noisy; treat the all-cores comparison as "about equal".
- Neither scales 12x: 4 of the 12 cores are efficiency cores.
- Go's case is not raw single-core speed. It is no Python process in the serving path, cheap goroutine concurrency, and one static binary. The query path (4 shifts, D15) can also run its shifts in parallel goroutines.

## B4: index size on fma_small (Phase 2)

6,997 tracks, 29,727,683 postings (identical count to the Python index, `eval/results/index_stats_splits_small_seed0.json`). Built by `cmd/ingest` at commit 5c3d71f (clean). Global-id sizes come from rewriting the local-id segments (`bench/b4remap`, commit ce6c110), since postings are identical. Raw: `bench/results/b4_*.json`.

| Config | Total bytes | Bytes per posting | Key + offset tables share |
|---|---|---|---|
| P=1, fixed 8 B postings | 270,977,892 | 9.115 | 12% |
| P=1, varint | 133,957,295 | 4.506 | 25% |
| P=4, varint, local ids | 185,103,763 | 6.227 | 46% |
| P=4, varint, global ids | | 6.472 (+3.9%) | |
| P=8, varint, local ids | 222,882,828 | 7.497 | 56% |
| P=8, varint, global ids | | 7.848 (+4.7%) | |

- Varint encoding halves the index (9.12 to 4.51 B/posting at P=1).
- Local ids (D19) save 3.9-4.7% over global ids, smaller than the "a byte per posting" estimate.
- **Per-key overhead grows with P.** Every partition stores its own copy of most keys (2.76M keys at P=1, 10.3M summed over 8 partitions), each costing 4 B key + 8 B offset. At P=8, 56% of bytes are lookup tables. Candidate fixes (not decided): u32 offsets (a segment's postings blob is far below 4 GiB), or delta-coded keys with a sparse in-memory key index.
- Implication for D20: at P=8 a partition holds ~1.3M of 16.7M possible keys (~8% dense), so a direct table (16.7M x 8 B = 134 MB per segment) would be several times larger here. Recheck at fma_large density.

## B1 quick sweeps on fma_small, and the vote-sort bottleneck (Phase 4)

**Setup (all runs):** single host, bare metal (shards and coordinator as local processes; Docker's 4-vCPU VM would cap throughput). Apple M4 Pro, 12 cores (8P + 4E), macOS, AC power. RF=1, P = 1/2/4/8 over the fma_small index (6,997 tracks), 200 ms coordinator deadline, 12 matcher workers per shard. Open-loop constant-rate load from `cmd/loadgen` replaying 1,499 pre-fingerprinted 10 s test-split queries (4 shifts, ~4,300 landmarks each); latency from each request's scheduled send time. **Quick methodology:** 5 s warmup, 15 s measured, 1 rep per point, meant to locate knees. Success = complete (non-partial) answers / requests sent. The load generator shares the machine, so P scaling here measures parallelism on one host, not distribution.

| P | Before D27: p50 / p99 at 100-400 qps | Before: last rate at 100% | After D27: p50 / p99 at 100-400 qps | After: last rate at 100% |
|---|---|---|---|---|
| 1 | 12.7-12.9 / 34-38 ms | 600 qps | 5.9-6.4 / 12.7-13.6 ms | 1,200 qps |
| 2 | 7.2-7.5 / 18-19 ms | 400 qps (600: 99.99%) | 3.9-5.0 / 7.8-9.5 ms | 800 qps |
| 4 | 4.5-5.4 / 10-15 ms | 400 qps | 2.9-4.8 / 5.2-7.8 ms | 800 qps |
| 8 | 3.7-5.1 / 8-33 ms | 400 qps | 2.7-4.7 / 4.9-7.0 ms | 600 qps |

Raw: `bench/results/b1_quick_small` (commit 3e1d3a9) and `bench/results/b1_quick_small_radix` (commit 3b7d8d2), both clean, AC. Plots: `docs/figures/b1_quick_small.png`, `docs/figures/b1_quick_small_radix.png`.

**What it shows**
- More partitions cut latency: one query's postings are split across P shards searched in parallel (p50 6.0 ms at P=1 to 2.8 ms at P=8 after D27).
- More partitions do not raise throughput on one machine: total work per query is unchanged, every partition adds fan-out cost on the same cores, so the knee falls as P grows. Throughput scaling needs partitions on separate machines.
- Past the knee, shards shed load (D23) and the coordinator returns partial answers or UNAVAILABLE rather than letting latency grow without bound; at P=8 the load generator itself competes for CPU, so client-side queueing pushes latency past the deadline.

**Finding the bottleneck** (`bench/results/prof_p4_small_*`)
- P=4 at 500 qps (below the knee): most shard CPU was Go runtime overhead (thread wake-ups via `pthread_cond_signal`, `kevent` netpolling, about 2/3 of samples). Capping GOMAXPROCS per shard only moved it from wake-ups to polling. This is idle schedulers spinning; it is not what limits throughput.
- P=4 at 700 qps (saturated): the overhead disappears; `Matcher.Search` is 76% of shard CPU, **54% in pdqsort** sorting votes.
- Fix (D27): byte-skipping LSD radix sort, 4.3x faster per sort, identical output. End to end: the knee roughly doubled at every P and low-load latency halved.
- Coordinator: ~46% of its CPU in network write syscalls, since each query's ~4,300 landmarks are re-sent to every partition as individual protobuf messages. A packed, struct-of-arrays request is the next lever.

## B5: ingest throughput vs workers (Phase 4)

600 fma_small tracks (first 600 of the indexed split), P=1, `cmd/ingest` with N decode+fingerprint goroutines (each runs its own ffmpeg process). Fingerprint phase timed separately from segment writing (~0.5 s). Apple M4 Pro, AC power, 3 reps; median and range. Raw: `bench/results/b5_small/rep{0,1,2}/`. (A first run on battery power gave similar numbers and was discarded.)

| Workers | Tracks/min, median (range) | Speedup |
|---|---|---|
| 1 | 793 (790-811) | 1.00 |
| 2 | 1,531 (1,518-1,539) | 1.93 |
| 4 | 2,870 (2,855-2,873) | 3.62 |
| 8 | 3,843 (3,841-3,868) | 4.85 |
| 12 | 4,118 (4,045-4,139) | 5.19 |

Near-linear to 4 workers, flattening past 8: the machine has 8 performance cores, and each worker drives an ffmpeg process as well as Go fingerprinting. Extrapolation, not a measurement: 106,574 fma_large tracks at ~4,100 tracks/min is about 26 minutes of fingerprinting on this laptop.
