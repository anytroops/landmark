# Benchmarks

Methodology: see `docs/PROJECT_BRIEF.md` section 14. Every number here traces to a file in `bench/results/`.

| ID | Benchmark | Result |
|---|---|---|
| B1 | Latency p50/p95/p99/p99.9 vs offered QPS, per partition count | quick sweeps on fma_small (below); full methodology on fma_medium [TBD] |
| B2 | Max sustainable QPS under p99 SLO vs partitions | [TBD] |
| B3 | Failure-injection timeline around a replica kill | single-host Docker, fma_small (below) |
| B4 | Index size: raw vs encoded, bytes per posting | fma_small and fma_medium (below); fma_large [TBD] |
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
- Coordinator: ~46% of its CPU in network write syscalls, since each query's ~4,300 landmarks are re-sent to every partition as individual protobuf messages.

**Second round** (`bench/results/prof_*_radix`): after D27, a lone partition (P=1, 1,400 qps) is bound by real matching work (Matcher.Search 93% of shard CPU, still mostly vote sorting), while at P=4 (1,000 qps) per-request overhead returns: syscalls, GC activity in shards, and ~52% syscalls plus ~25% protobuf in the coordinator. Fix (D28): send landmarks as packed `hashes`/`times` arrays, 40% fewer bytes per request (`bench/results/msgsize.json`) and two slices to decode instead of ~3,800 objects. Same quick sweep, before and after:

| P | p50 at 300-800 qps: D27 / D27+D28 | Last rate at 100%: D27 / D27+D28 |
|---|---|---|
| 1 | 5.9-6.0 / 5.4 ms | 1,200 / 1,600 qps |
| 2 | 3.9-4.1 / 3.4-3.5 ms | 800 / 1,200 qps |
| 4 | 2.9-3.9 / 2.3-2.4 ms | 800 / 800 qps (1,200: 8.0% to 99.99%) |
| 8 | 2.7-5.4 (300-600) / 2.0-2.1 (300-600) ms | 600 / 800 qps |

Raw: `bench/results/b1_quick_small_packed` (commit 9143080, clean, AC), plot `docs/figures/b1_quick_small_packed.png`. (A first run of this sweep happened before D28 was committed, because a lint failure stopped the commit; it was discarded, and the bench scripts now refuse uncommitted code.) From the first sweep to D27+D28, a single partition went from 600 to 1,600 qps at 100% complete answers and from 12.7 to 5.4 ms p50 on the same machine.

## B4 at fma_medium scale, and the hash-space ceiling

23,985 tracks, 102,118,846 postings, varint, local ids. Built in one fingerprint pass by `cmd/ingest -partitions 1,2,4,8` (commit 107f231). Sizes are deterministic; the ingest timings in these files are not usable (the Mac slept repeatedly during the run). Raw: `bench/results/b4_medium_p{1,2,4,8}.json`.

| Subset | P | Postings | Keys (summed over segments) | Bytes | Bytes/posting | Key+offset share |
|---|---|---|---|---|---|---|
| small | 1 | 29,727,683 | 2,763,032 | 133,957,295 | 4.506 | 25% |
| medium | 1 | 102,118,846 | 3,343,978 | 387,623,299 | 3.796 | 10% |
| medium | 2 | 102,118,846 | 6,083,997 | 420,070,100 | 4.114 | 17% |
| medium | 4 | 102,118,846 | 10,505,735 | 472,590,950 | 4.628 | 27% |
| medium | 8 | 102,118,846 | 17,103,441 | 550,593,699 | 5.392 | 37% |

- Postings grew 3.4x but distinct keys only 1.2x, so per-key overhead is amortized and bytes per posting fall (4.51 to 3.80 at P=1).
- **The reachable hash space is nearly full.** D10 pairs peaks within +/-64 bins, so a hash can take about 512 x 129 x 63 = 4.16M values, not 2^24. fma_medium already uses 3.34M (~80%). From here, new tracks mostly lengthen existing posting lists, so per-query postings scanned, and query cost, grow roughly linearly with catalog size. For fma_large this argues for revisiting D20 (a direct table over ~4.2M reachable keys is dense) and the hash layout (more bits per landmark to keep lists short). Measure before deciding.
