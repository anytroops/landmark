# Benchmarks

Methodology: see `docs/PROJECT_BRIEF.md` section 14. Every number here traces to a file in `bench/results/`.

| ID | Benchmark | Result |
|---|---|---|
| B1 | Latency p50/p95/p99/p99.9 vs offered QPS, per partition count | full methodology on fma_medium, P=1/2/4/8 (below); quick sweeps on fma_small and fma_medium |
| B2 | Max sustainable QPS under p99 SLO vs partitions | fma_medium, one host: P=1/2/4/8 sustain 550/472/464/374 qps (below); P=1's limit varies about 520-550 between sessions. With the stop list (D30) at 2%: P=1 925 qps |
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

## B3: replica kill under load (Phase 3 done criterion)

Docker compose cluster (`deploy/docker-compose.yml`): P=2 partitions x RF=2 replicas over the fma_small index, coordinator with a 200 ms deadline and D24-D26 failure handling. Open-loop 100 qps from `cmd/loadgen` (1,499 replayed test-split queries), 10 s warmup, 60 s measured; `docker kill -s KILL landmark-shard-1a-1` fired at t = 30.00 s on the load generator's clock. 3 reps, commit 44dc5e1 (clean), AC power. Docker runs in a 4-vCPU Colima VM on the same laptop as the load generator. Raw: `bench/results/b3_rate100_rep{0,1,2}/`; plot `docs/figures/b3_rate100.png`.

| Rep | Complete answers (measured 6,000) | p50 | p99 | Max | Requests over 50 ms | p99 in the kill second |
|---|---|---|---|---|---|---|
| 0 | 100.00% | 5.81 ms | 9.95 ms | 109.2 ms | 3 | 105.7 ms |
| 1 | 100.00% | 5.81 ms | 9.97 ms | 110.4 ms | 3 | 106.3 ms |
| 2 | 100.00% | 5.81 ms | 9.96 ms | 107.1 ms | 2 | 105.9 ms |

- A killed container's address vanishes, so requests to it hang rather than fail. Each of the 2-3 slow requests waited out its first attempt's half-budget timeout (100 ms of 200 ms, D24) and then succeeded on the partner replica.
- After 3 consecutive failures the replica was ejected (D25), so no further requests went to it: the very next second's p99 is back to ~9 ms.
- No query failed or came back partial. Partial answers on whole-partition loss are covered by the coordinator tests (`TestWholePartitionDownIsPartialNotWrong`) and the manual smoke test.

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
- **The reachable hash space is nearly full.** D10 pairs peaks within +/-64 bins, so a hash can take about 512 x 129 x 63 = 4.16M values, not 2^24. fma_medium already uses 3.34M (~80%), so a direct table over the reachable keys (D20 option B) would now be dense. Fullness does not by itself make query cost grow faster: cost grows linearly with catalog size at any fill level (measured in "Postings scanned per query" below, which corrects an earlier version of this note).

## B1 quick sweep on fma_medium (Phase 4)

Same harness and quick methodology as the fma_small sweeps (5 s warmup, 15 s measured, 1 rep), D27 + D28 code, over the fma_medium indexes (23,985 tracks, 102M postings) with 1,500 medium test-split replay queries. Commit 361c684, clean, AC. Raw: `bench/results/b1_quick_medium`; plot `docs/figures/b1_quick_medium.png`.

| P | p50 / p99 at 100-200 qps | Last rate at 100% complete | First rate below 99.9% |
|---|---|---|---|
| 1 | 13.5-14.0 / 34.7-34.8 ms | 400 qps | 600 qps (95.9%) |
| 2 | 8.4-8.6 / 19.8-44.8 ms | 400 qps | 600 qps (71.2%) |
| 4 | 5.0-5.5 / 10.8-11.9 ms | 400 qps (p99 104.5 ms) | 600 qps (14.6%) |
| 8 | 3.8-4.5 / 8.2-8.3 ms | 200 qps | 400 qps (98.3%) |

- Per-query work is ~2.8x fma_small's: postings per key rose from ~10.8 to ~30.5 as the reachable hash space filled (see B4 at medium), and low-load p50 rose by about that factor (P=1: 5.4 to 13.5 ms).
- Knees fall correspondingly (P=1: 1,600 qps on small to ~400-600 here). Partitioning still trades throughput for latency on one host.

## B2: max sustainable throughput under the SLO, fma_medium (Phase 4)

SLO: p99 <= 100 ms and >= 99.9% complete answers, latency from scheduled send time (open loop). `bench/scripts/b2_slo.py`: bisect with 60 s probes, then confirm with the full methodology (30 s warmup, 120 s measured, 3 reps); a rate is confirmed when the median of the reps meets the SLO (D29), stepping down 5% otherwise. Single host, bare metal, RF=1, fma_medium indexes (23,985 tracks), 1,500 replay queries, 200 ms coordinator deadline. Commit 1479a69, clean, AC power with the battery at 100% and not charging, no sleep events (both checked against `pmset -g log`). Raw: `bench/results/b2_medium/P{1,2,4}/`; plot `docs/figures/b2_medium.png`.

| P | Max sustainable | p50 at that rate (median) | p99 median (each rep) | Complete answers (each rep) |
|---|---|---|---|---|
| 1 | **550 qps** | 22.8 ms | 69 ms (67, 69, 153) | 100.00%, 100.00%, 99.52% |
| 2 | **472 qps** | 9.5 ms | 59 ms (171, 59, 24) | 98.70%, 100.00%, 100.00% |
| 4 | **464 qps** | 6.9 ms | 40 ms (201, 40, 32) | 89.55%, 99.95%, 100.00% |
| 8 | **374 qps** | 4.8 ms | 58 ms (84, 58, 18) | 99.96%, 99.98%, 100.00% |

- The rep spread is wide near the knee: at each confirmed rate, one of three 120 s runs breached the SLO (p99 153, 171, and 201 ms for P = 1, 2, 4; P=4's was also only 89.6% complete) while the other two were well inside it. That is what running at ~90% of capacity looks like: bursts of arrivals briefly queue. A rate where every rep passes would be lower; D29 explains the median choice.
- Throughput does not grow with P on one machine (550, 472, 464 qps), while latency at the rate falls (p50 22.8 ms at P=1 vs 6.9 ms at P=4). Same trade as the B1 sweeps: partitions parallelize a query, but on one host they share the same 12 cores and each query pays for P shard RPCs instead of one.
- The knee is sharp. A few percent above the confirmed rate, median latency heads for the 200 ms deadline (60 s probes): P=1 goes from p50 23 ms at 550 qps to 130 ms at 581; P=4 from 11 ms at 488 to 195 ms at 600. Once arrivals outpace what the cores can serve, the queue grows until the deadline cuts requests off, so losing a few percent of capacity turns a passing rate into a failing one (see the charging note below).
- P=8 ran at commit e33b868 with the battery guard: every run started and ended at "100%; charged" (`harness.json`). Two earlier P=8 runs were set aside: one on battery with the Mac sleeping (discarded), and one while the battery charged from 82% (commit 0b9523f, 394 qps, kept in `bench/results/b2_medium_charging/P8`). That charging run came out 5% higher, not lower, which is within session-to-session variation.

## Charging confound (Phase 4)

The first full-method B1 attempt (commit 0e5a3be) ran P=1 while the battery charged from 82% to 99% on the laptop's 65 W adapter. At 550 qps, the rate B2 had confirmed at full charge with the same code and settings, all three 120 s runs were overloaded: p50 128 to 135 ms, p99 183 to 197 ms, 95.6% to 97.1% complete answers. B2's confirm runs at that rate had p50 22.7 to 26.9 ms. Below the knee the charging runs looked normal (p50 14.6 to 16.3 ms at 138 to 412 qps, 100% complete), which fits a capacity loss of a few percent: invisible at light load, decisive at the knee. Raw: `bench/results/b1_charging_medium`. This is a comparison across sessions, so charging is the likely cause, not a proven one; macOS recorded no thermal warnings.

The full-charge rerun (commit e33b868, `bench/results/b1_full_medium`) settles part of it. P=1 at 550 qps did not collapse: p50 22.5, 42.7, 48.3 ms, p99 66, 140, 123 ms, 99.95% to 100% complete. But the median p99 (123 ms) misses the SLO in this session, where B2's confirm runs passed with 69 ms. So charging turned a rate at the edge into a collapse, and P=1's limit on this laptop is about 520 to 550 qps depending on the session. Within each set of three runs, latency rose from run to run (here and in B2's confirm runs), which may be the laptop heating under sustained load; macOS recorded no thermal warnings, so that is unconfirmed.

Since commit fe4c470 the harness waits for the battery to stop charging before every run, records battery state before and after (`harness.json`; `machine.battery` in `summary.json`), and marks a run invalid if the battery drained or began charging during it. That also tests whether the 65 W adapter keeps up with full load: a drain on AC would invalidate the run.

## Postings scanned per query vs catalog size (hash-space check)

`bench/postings` (commit 4d2fb2a, clean; `bench/results/postings_medium.json`) keeps a seeded random subset of the fma_medium P=1 index's tracks (nested subsets) and counts, for each of the 1,500 test-split queries (3,829 landmarks on average, 4 shifts), the postings under its hashes. That count is the number of votes the matcher sorts, its main cost (B1 profiles).

| Tracks | Keys in use (of 4.16M reachable) | Mean list length | Postings scanned per query, mean / p99 | Per 1,000 tracks |
|---|---|---|---|---|
| 1,499 | 1.64M (39%) | 3.9 | 48,202 / 123,775 | 32,156 |
| 2,998 | 2.12M (51%) | 6.0 | 97,749 / 255,955 | 32,605 |
| 5,996 | 2.62M (63%) | 9.7 | 193,720 / 499,862 | 32,308 |
| 11,992 | 3.04M (73%) | 16.8 | 385,200 / 991,613 | 32,121 |
| 23,985 | 3.34M (80%) | 30.5 | 773,850 / 2,016,568 | 32,264 |

- **Query cost grows linearly with catalog size at every fill level**: about 32,000 postings per query per 1,000 tracks whether 39% or 80% of the reachable keys are in use. How full the key space is does not cause the growth (the B4 note above said otherwise before this measurement); the number of distinct values a query's hashes can take sets the slope. Projected to fma_large's 106,574 tracks: about 3.4M postings per query, 4.4x medium (a projection, not measured).
- **Queries hit the long lists.** A query landmark's list averages 202 postings (773,850 / 3,829), 6.6x the mean list length of 30.5.
- **The cost sits in few keys.** The longest 0.1% of lists (length >= 879) produce 32% of all postings scanned while serving 4.2% of query landmarks; the longest 1% produce 64% while serving 17%.
- **Those keys are held notes and low frequencies.** Of the longest 0.1%, 24% pair two peaks at the same frequency (0.8% of all keys), 32% pair peaks within 4 frames, ~93 ms (6.3% of all keys), and 41% anchor below bin 64, ~690 Hz (9.0% of all keys). These patterns genuinely repeat across songs, so finer quantization (a wider hash) would not split them.
- **Skipping long lists** (a stop list, as text search does with "the"): a cap of 1,024 postings keeps 96.4% of query landmarks and removes 28% of postings scanned; 512 keeps 90.5% and removes 48%; 256 keeps 79.6% and removes 68%. The effect on accuracy is not measured yet.

## B1: latency vs load, full methodology, fma_medium (Phase 4)

Each P at 25, 50, 75, and 100% of its B2 rate; 30 s warmup, 120 s measured, 3 reps per point; open loop; single host, bare metal, RF=1, 1,500 replay queries, 200 ms deadline. Every run started and ended at "100%; charged" (`harness.json`, battery guard). Two sessions on identical code: commit e33b868 (P=1, and P=2 up to 354 qps; night of 2026-10-16) and eb6a96a (P=2 at 472 qps, P=4, P=8; 2026-10-17 afternoon; only docs and results changed in between). Raw: `bench/results/b1_full_medium` (48 runs); plot `docs/figures/b1_full_medium.png`.

p50 / p99 in ms, medians of 3 runs (p99 range across runs where it exceeds 2 ms):

| P | 25% | 50% | 75% | 100% (B2 rate) |
|---|---|---|---|---|
| 1 | 138 qps: 14.7 / 36.6 | 275 qps: 14.6 / 36.3 | 412 qps: 15.5 / 38.2 | 550 qps: 42.7 / 123 (66 to 140) |
| 2 | 118 qps: 8.7 / 19.5 | 236 qps: 8.4 / 19.2 | 354 qps: 8.5 / 19.5 | 472 qps: 9.9 / 48 (27 to 49) |
| 4 | 116 qps: 5.7 / 11.4 | 232 qps: 5.2 / 10.9 | 348 qps: 5.2 / 11.2 | 464 qps: 6.3 / 25 (24 to 29) |
| 8 | 94 qps: 5.3 / 9.1 | 187 qps: 3.6 / 6.7 | 280 qps: 3.4 / 7.4 | 374 qps: 4.4 / 23 (18 to 35) |

Complete answers were 100% in every run except one P=1 run at 550 qps (99.95%) and one P=8 run at 374 qps (99.99%).

- **Up to 75% of each B2 rate, latency is flat and repeatable**: the three runs at each of those points agree within 1 ms at p99. All the variation sits in the last 25%, at the knee.
- **At similar load, partitioning cuts latency about 4 to 5x on one host**: at 232 to 280 qps, p50 falls from 14.6 ms (P=1) to 8.4 (P=2), 5.2 (P=4), and 3.4 ms (P=8), and p99 from 36.3 to 19.2, 10.9, and 7.4 ms. Each partition scans 1/P of a query's postings in parallel.
- **At its B2 rate, P=1 is on the knee** (p99 66 to 140 ms across runs, so the median misses the SLO in this session; see the charging note), while P=2, P=4, and P=8 stay well inside the SLO at theirs (p99 at most 49 ms). Their B2 confirm runs each had one run breach the SLO; these did not. Rates at the edge vary from session to session.
- **Light load is slightly slower than moderate load at P=4 and P=8** (P=8 p50 5.3 ms at 94 qps, 3.4 ms at 280 qps). This is consistent with the thread wake-up overhead the below-knee profiles showed: at low rates, idle threads sleep between requests and pay to wake. Not separately measured.

## B2 with the stop list (D30), fma_medium, P=1 (Phase 4)

Same harness and SLO as B2 (bisect with 60 s probes, confirm with 3 x 120 s runs, median rule D29), coordinator `stop_fraction` 0.02 and 0.04, P=1, every run at "100%; charged". The baseline (stop list off) was rerun in the same session right after, so the ratios compare like with like. Commits e43d1ea (stop list) and 33e9ca1 (baseline; docs and results only in between). Raw: `bench/results/b2_medium_stop{0.02,0.04}/P1/`, `bench/results/b2_medium_sameday/P1/`. Accuracy per cutoff is in EVAL.md (stop list section).

| Stop list | Max sustainable | p50 at that rate (each run) | p99 (each run) | Postings scanned per query, clean 10 s (median) | SNR 0 dB accuracy |
|---|---|---|---|---|---|
| off (same session) | 550 qps | 21.3, 20.9, 20.5 ms | 85, 86, 113 ms | 689k | 97.1% |
| 4% of tracks | **719 qps** | 15.7, 15.8, 15.0 ms | 85, 113, 33 ms | 524k | 95.5% |
| 2% of tracks | **925 qps** | 11.5, 11.7, 10.8 ms | 90, 142, 45 ms | 388k | 94.4% |

- Throughput rises 1.3x at 4% and 1.7x at 2% against the same-session baseline, roughly in line with the drop in postings scanned (24% and 44%): vote sorting is the main per-query cost. The same-session baseline confirmed 550 qps, the same rate as the first B2 run a day earlier.
- At the same load latency falls too: at 400 qps (60 s probes) p50 / p99 were 9.6 / 12.7 ms at 2% and 12.2 / 20.7 ms at 4%, against 15.5 / 38.2 ms at 412 qps with the stop list off (B1).
- Each confirmed rate is again at the edge (one of three runs breached p99 at each), as with every B2 point.
- The price is accuracy in heavy noise (EVAL.md); clean and compressed audio are unchanged. Which cutoff to run by default is a product choice; the coordinator default stays off.

## Before/after D27 + D28 at full methodology, fma_medium, P=1 (Phase 4)

The quick sweeps on fma_small (above) put the D27 (radix vote sort) + D28 (packed request arrays) gain at 2.7x. This repeats it on fma_medium with the full methodology. "Before" is commit 8f76046 (just before D27), built in a separate worktree and run under the current harness via `LANDMARK_BIN` (each run's `harness.json` records `bin_repo_commit` 8f76046); "after" is commit f83d491. Same session, back to back, every run at "100%; charged". Script: `bench/scripts/before_after.sh`. Raw: `bench/results/before_after_medium/{before,after}/`, `bench/results/b2_capacity_medium_{before,after}/P1/`.

| | Before (8f76046) | After (f83d491) | Change |
|---|---|---|---|
| p50 at 100 qps (3 x 120 s, median) | 39.1 ms | 14.8 ms | 2.6x lower |
| p99 at 100 qps | 108.3 ms | 36.2 ms | 3.0x lower |
| p99.9 at 100 qps | 147.6 ms | 48.4 ms | 3.0x lower |
| Capacity: max rate with >= 99.9% answered within the 200 ms deadline (B2 search, median of 3) | 170 qps | 398 qps | 2.3x |

- **Latency is the robust number**: the three runs on each side agree within 0.6 ms at p50 and 0.7 ms at p99.
- **The old code cannot meet the 100 ms p99 SLO at any load**: a single probe at 25 qps already had p99 100.1 ms (`bench/results/b2_medium_pre_d27/P1/probe_r25`), because its slowest 1% of queries take about 100 ms of service time on their own. That is why capacity is compared against the deadline instead.
- **The capacity ratio is noisy, and 2.3x is a low estimate.** In this run, the "after" code failed 412 qps on completeness (99.63%: 91 requests shed or timed out) with p99 136 ms, yet it sustained 550 qps under the stricter 100 ms SLO about 90 minutes earlier (`b2_medium_sameday`). The capacity search ran after nearly an hour of continuous load, including overload probes at 1,200 and 750 qps just before; heat is a plausible cause but unconfirmed (no backup was running, macOS recorded no thermal warnings). Against that earlier result the gain would be 3.2x. Claim "more than doubled".


## B6: client fingerprinting cost per DJ-mix window, pairs vs B2 (D35, D36)

`bench/querycost`: 100 fma_medium test positives, one 10 s window each (the cmd/loadgen excerpt offsets), 4 sub-hop shifts, one core, each figure the median over windows of the fastest of 3 passes. Commit 5faea30, clean, AC power with the battery not charging. Raw: `bench/results/b5_querycost.json`.

| Per 10 s window | Median | p90 | Landmarks (median) |
|---|---|---|---|
| Pair query fingerprint (one search's worth) | 31.9 ms | 32.2 ms | 3,906 |
| Speed grid: 17 pair fingerprints (unchanged + 16 turntable steps; resampling not timed) | >= 541.6 ms | >= 548.0 ms | |
| B2 dual-filterbank query fingerprint | 126.0 ms | 127.2 ms | 13,978 |

Index side, per 30 s track: pairs 25.2 ms, B2 48.3 ms (1.9x).

- **B2 costs 4x one pair fingerprint but is 4.3x cheaper than the grid's fingerprinting** per window; the hybrid's plain-window pair (D36) adds 31.9 ms, about 158 ms in all. The hybrid's double- and half-time turntable searches fingerprint the window resampled to 20 s and 5 s; not timed here.
- Shard-side throughput under the SLO for B2 searches is not measured yet: the run stopped when the machine went on battery.

## B7: shard throughput, B2 vs pair searches (D35, D36)

`bench/scripts/b2_slo.py`, single host, bare metal, P=1, RF=1, stop list 4%, the fma_medium indexes extended with the 302 UnmixDB references (pairs: `medium_unmixdb`, 102M postings; B2: `medium_unmixdb_b2`, 182.5M). Queries: the 1,500 medium test-split 10 s excerpts, fingerprinted as pairs (`medium_test_10s_seed0.gob`) or as dual-filterbank B2 triplets (`medium_test_10s_seed0_b2.gob`, 3.6x the landmarks). Commit 211a7a3, clean, every run at "100%; charged". Raw: `bench/results/b5_b2_slo`, `b5_b2_batch`, `b5_pairs_batch`.

- **The interactive SLO (p99 <= 100 ms) is out of reach for B2 at P=1**: the lowest probe, 20 qps, had p50 70 ms and p99 128 ms (`b5_b2_slo`).
- **With a batch target** (p99 <= 1 s, >= 99.9% complete, 2 s deadline; DJ-mix tracklisting is offline work), same session, median rule (D29):

| Search | Max sustainable | p50 at that rate (each run) | p99 (each run) | Complete answers (each run) |
|---|---|---|---|---|
| B2 (dual query) | **118 qps** | 114, 116, 120 ms | 222, 247, 239 ms | 100.00%, 99.89%, 99.94% |
| Pairs | **697 qps** | 17.4, 16.6, 16.0 ms | 68, 99, 42 ms | 100.00%, 99.99%, 100.00% |

- **One B2 search costs about 5.9 pair searches** of shard capacity: B2 queries carry 3.6x the landmarks (two filterbanks) and each match votes in up to 4 scale bins before the line fits. Both fail by shedding (incomplete answers) just above these rates, not by latency.
- **Cost per DJ-mix window** at P=1, from these rates (each search counted at its 10 s-query cost): the speed grid's 37 pair searches allow about 19 windows per second; the hybrid's 3 B2 + 3 pair searches about 34 (1.8x); the plain-window hybrid's 1 + 1 about 100 (5.3x), at 94.1% of UnmixDB songs instead of 95.9% (EVAL). The hybrid's x2 search fingerprints a 20 s resampled window, so its true cost is somewhat higher than counted.
- B2's search cost is the next optimization target; levers include fewer scale bins per match, a stop list tuned to triplet hashes, and more partitions per query.
