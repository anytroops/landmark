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

## B8: pruned B2 searches (D37)

Same harness and configuration as B7 (P=1, RF=1, stop list 4%, same indexes and query sets), B2 searches pruned to the 200 tracks with the most matches. One session, every run at "100%; charged", commit 063d508. Raw: `bench/results/b8_b2p_slo`, `b8_b2p_batch`, `b8_pairs_batch`.

| Search, target | Max sustainable | p50 at that rate (each run) | p99 (each run) | Complete answers (each run) |
|---|---|---|---|---|
| B2 pruned, interactive (p99 <= 100 ms) | **378 qps** | 32.1, 31.7, 32.6 ms | 75, 73, 93 ms | 100.00% (all) |
| B2 pruned, batch (p99 <= 1 s, 2 s deadline) | 286 qps | 23.3, 23.6, 23.9 ms | 39, 39, 40 ms | 100.00% (all) |
| Pairs, batch | 734 qps | 17.6, 17.5, 17.7 ms | 48, 48, 50 ms | 100.00% (all) |

- **Pruning makes a B2 search 2.4-3.2x cheaper**: 286-378 qps against 118 (B7), and it now meets the interactive SLO, which unpruned B2 missed even at 20 qps.
- **The two B2 numbers are one knee seen twice**: the batch search's probe at 298 qps shed 0.3% of requests (completeness, not latency, failed), so it confirmed 286; the interactive search, run first, confirmed 378 with every run passing. As in B2, a few percent of offered load decides pass or fail near capacity.
- **One B2 search now costs about 1.9-2.6 pair searches** (734 / 378 to 734 / 286; 5.9 before). Per DJ-mix window at P=1: the speed grid's 37 pair searches allow about 20 windows per second; the hybrid's 3 B2 + 3 pair searches 69-83 (3.5-4.2x the grid); the two-search hybrid 206-250 (10-13x), at 93.7% of UnmixDB songs against the hybrid's 95.2% (EVAL, D37).

## B9: live monitoring in real time (D38-D40)

One worker (`cmd/monitor`) monitoring N stations, each playing 3 UnmixDB test mixes back to back in real time from `cmd/streamserver`, against the triplet and pair clusters (P=1, stop list 4%) on the same laptop. Every window gets the hybrid's 6 searches. Lag is the time from a window's last sample arriving to its searches finishing; it must stay under the 2.5 s hop, or windows queue up. Detection delay runs from each song's true transition (middle of its crossfade) to its start event. Commit 61f245e (16 and 32 stations: 0436f83, rerun after power changes invalidated the first runs; the monitoring code is the same), AC power, battery "100%; charged". Raw: `bench/results/live/` (its README lists the runs invalidated by power changes).

| Stations | Window lag p50 / p99 | Songs found | Precision | Detection delay p50 / p90 |
|---|---|---|---|---|
| 16 | 0.9 / 1.0 s | 137/143 (95.8%) | 99.3% | 12.0 / 17.8 s |
| 24 | 1.4 / 1.7 s | 205/215 (95.3%) | 98.6% | 12.6 / 21.1 s |
| 32 | 2.0 / 2.2 s | 273/286 (95.5%) | 98.9% | 12.9 / 19.4 s |
| 48 | 53 / 109 s | 406/428 (94.9%) | 99.0% | 61.1 / 107.2 s |
| 64 | did not finish: 6 of 64 streams reached their end in the time limit | | | |

- **One laptop keeps up with 32 stations** in real time, just: window lag p99 2.2 s against the 2.5 s hop. At 48 the worker falls behind steadily: every window is still answered (none unanswered), so accuracy holds, but songs are reported a minute or more late; at 64 it never caught up. Lag grows about linearly with stations (p50 0.9, 1.4, 2.0 s at 16, 24, 32): each window's fingerprinting and 6 searches share the same 12 cores.
- **After D44** (one FFT per frame for both query filterbanks, B13; commit 12e1cae, same harness and settings, AC power, battery "100%; charged"; `rt_cap48_d44`, `rt_cap56_d44`):

| Stations | Window lag p50 / p99 | Windows later than the hop | Songs found | Precision | Detection delay p50 / p90 |
|---|---|---|---|---|---|
| 48 | 1.9 / 2.3 s | 1 of 5,942 | 406/428 (94.9%) | 99.0% | 12.8 / 17.1 s |
| 56 | 2.3 / 5.8 s | 2,092 of 6,892 | 473/499 (94.8%) | 98.8% | 13.4 / 17.9 s |

  **One laptop now keeps up with 48 stations** (lag p99 2.3 s, as 32 did before), where it previously fell a minute or more behind. At 56 it is saturated: the median lag stays under the hop for the whole run and does not grow, but 30% of windows finish later than the hop (p99 5.8 s), so there is no headroom. Accuracy is unchanged, as D44 changes no landmark: at 48 stations every stream's event log has the same songs, starts, and ends (relative to the stream's first event) as `rt_cap48` before D44, now reported on time.
- **After D45** (a live source that drops is joined again; only 410 Gone ends it), 16 stations at a48a504 (`rt_cap16_d45`): every stream's event log has the same songs, starts, and ends as `rt_cap16`; each station's end now reads as a drop, one try to rejoin, and 410 Gone (16 of each in the worker log). Window lag p50 0.6 s against 0.9 s before D44. The worker kill repeated at a4c7288 (`rt_kill16_d45`): again all 16 streams moved to the standby with no sequence gaps, 15 resumed and 1 interrupted, 137 of 143 songs found at 99.3% precision; 5.6 s of audio lost per stream (5.2 s before: it depends on where the kill falls between heartbeats).
- **After D48** (the shared front end, windows every 27,520 samples; commit 1c9d98a, same harness, AC power, battery "100%; charged"; `rt_cap48_d48`, `rt_cap64_d48`, `rt_cap80_d48`, `rt_kill16_d48`):

| Stations | Window lag p50 / p99 | Windows later than the hop | Windows unanswered | Songs found | Precision |
|---|---|---|---|---|---|
| 48 | 0.7 / 1.0 s | 0 of 5,952 | 0 | 407/428 (95.1%) | 98.6% |
| 64 | 1.1 / 1.7 s | 0 of 7,874 | 501 (6.4%) | 542/570 (95.1%) | 98.6% |
| 80 | 1.9 / 7.1 s | 2,604 of 9,809 | 1,257 | 671/711 (94.4%) | 98.8% |

  Fingerprinting is no longer the limit: at 48 stations the lag is under half what it was after D44 (p99 1.0 s against 2.3 s), and at 64 it stays under the hop. What fails at 64 is the shards: every unanswered window is load shedding ("no free worker within 100 ms"). The test stations all broadcast in lockstep from one start time, so the streams' windows are ready together (79% within 0.1 s, 93% within 0.2 s, of one moment in each hop) and 64 x 6 searches arrive at once at shards with 8 workers each; with per-window fingerprinting, about 300 ms of CPU per window spread them out. Songs found still 95.1%, as the decoder rides over a missing window. The worker kill at 16 stations behaves as before (16 streams moved, no sequence gaps, 15 resumed and 1 interrupted, 5.2 s of audio lost each; window lag p99 0.33 s).
- **After D49** (the worker caps windows searched at once and jitters retries; same harness, AC power, battery "80%; AC attached; not charging"; cap 16 at 1d2cf3c, the others at 9d1bbde and ebdf59f, which change only the harness and the default; `rt_cap{64,80,96}_d49`, `rt_cap{64,80,88,96}_d49_f32`, `rt_cap80_d49_f24`, `rt_cap80_d49_f0`):

| Stations | Cap | Window lag p50 / p99 | Windows later than the hop | Windows unanswered | Songs found | Precision |
|---|---|---|---|---|---|---|
| 64 | 16 | 1.1 / 1.9 s | 0 of 7,874 | 0 | 543/570 (95.3%) | 98.6% |
| 64 | 32 | 0.9 / 1.4 s | 0 of 7,874 | 0 | 543/570 (95.3%) | 98.6% |
| 80 | none | 1.4 / 2.0 s | 0 of 9,809 | 502 | 676/711 (95.1%) | 98.7% |
| 80 | 16 | 38.7 / 74.5 s | 9,527 of 9,809 | 0 | 676/711 (95.1%) | 98.7% |
| 80 | 24 | 1.2 / 1.9 s | 1 of 9,809 | 0 | 676/711 (95.1%) | 98.7% |
| 80 | 32 | 1.2 / 2.0 s | 0 of 9,809 | 0 | 676/711 (95.1%) | 98.7% |
| 88 | 32 | 1.3 / 2.1 s | 0 of 10,849 | 0 | 742/783 (94.8%) | 98.5% |
| 96 | 32 | did not keep up: lag p99 159 s, 84 unanswered, 618/851 songs by the time limit | | | | |

  **One laptop now keeps up with 88 stations** with every window answered (lag p99 2.1 s, detection delay p50 12.1 s), where D48 shed 6.4% of windows at 64 and 12.8% at 80. The cap does it: the jittered retries alone (no cap) still lose 502 windows at 80 stations, though fewer than D48's 1,257 and with lag p99 2.0 s against 7.1 s. A cap of 16, the shards' 8 + 8 search workers, is too tight: a window holds its slot through its six searches and the pair fingerprinting between them, so 16 windows left the shards idle part of the time, and at 80 stations they answered 22.6 windows a second against the 31.6 that reached the worker (32.0 with the cap at 24 or 32; `python -m bench.scripts.live rates`, over the middle half of each run); the worker fell a minute behind with no window lost. 32 is the default (D49). At 96 stations the worker gets through fewer windows a second than at 88 (25.3 against 35.2), each stream's 64-window buffer fills, and it stops reading its audio (gaps of up to 105 s between a stream's windows; only 22.6 windows a second reach it), so the harness's stall check rejects the timings and the measured lag understates the real delay; with the cap at 16 it fell behind the same way (`rt_cap96_d49`, 9.3 answered a second). Past capacity, a worker that sheds nothing falls further behind instead of dropping windows, and its throughput drops rather than holding at the maximum.
- **After D50** (a live window still waiting for a search slot 2.5 s after its audio arrived is dropped; commit e16ac50, cap 32, same harness, AC power, battery "80%; AC attached; not charging"; `rt_cap{88,96,112}_d50`). Lag counts a dropped window at the moment it is dropped.

| Stations | Window lag p50 / p99 | Windows dropped | Answered a second (ready a second) | Songs found | Precision | Detection delay p50 / p90 |
|---|---|---|---|---|---|---|
| 88 | 1.4 / 2.1 s | 0 of 10,849 | 35.2 (34.7) | 742/783 (94.8%) | 98.5% | 12.2 / 17.1 s |
| 96 | 2.5 / 5.5 s | 6,037 of 11,787 (51%) | 15.4 (37.8) | 729/851 (85.7%) | 99.2% | 18.5 / 34.0 s |
| 112 | not valid: the worker stopped reading audio for up to 17 s; 7,669 of 11,527 windows dropped, 10.1 answered a second, 580/992 songs (58.5%) | | | | | |

  Below capacity nothing changes: at 88 stations no window is dropped and every stream's event log equals the D49 run's (`rt_cap88_d49_f32`), evidence counts included. At 96 the worker now keeps up with its audio (the longest gap between a stream's windows is 4.8 s, against 105 s without dropping) and reports songs within about half a minute (detection delay p90 34.0 s), with precision intact, since a dropped window loses evidence but adds no wrong answer. But it drops half the windows, so it finds 85.7% of songs and splits 115 into two entries. And it answers only 15.4 windows a second, under half of what it managed at 88: dropping removed the backlog but not the collapse, so each window costs more when the load is higher.
- **Why throughput collapses past capacity.** Diagnostic runs at b6f0d1e (D50; a worker CPU profile over 30 s, 200 s into the run, and `top` every 5 s; not timed results, raw in `bench/results/live/diag_d50/`): at 96 stations the worker spent 56% of its CPU in `runtime.memclrNoHeapPointers` and `runtime.memmove` (49% at 88), zeroing and copying freshly allocated memory, with fingerprinting itself most of the rest. The machine had 9 to 11 GB of memory compressed and spent 30 to 37% of all CPU in the kernel (6 to 8 GB and 6 to 21% at 88): each window allocated about 112 MB of spectrograms and max-filter buffers afresh (B17), some 4 GB a second at 88 stations, on a laptop with about 12 GB already in use by other applications. D51 keeps those buffers from one window to the next.
- **After D51** (fingerprinting buffers kept between windows; commit 78de07e, cap 32, drop after 2.5 s, same harness, AC power, battery "100%; charged"; `rt_cap{88,96,104,112}_d51`, each with the machine's CPU and memory lines from `top` every 30 s in `machine.txt`):

| Stations | Window lag p50 / p99 | Windows later than the hop | Windows dropped | Answered a second (ready a second) | Songs found | Precision | Detection delay p50 / p90 |
|---|---|---|---|---|---|---|---|
| 88 | 1.2 / 1.9 s | 0 of 10,849 | 0 | 35.2 (34.7) | 742/783 (94.8%) | 98.5% | 12.0 / 16.8 s |
| 96 | 1.4 / 2.4 s | 83 of 11,787 | 0 | 38.3 (37.8) | 803/851 (94.4%) | 98.7% | 12.2 / 16.9 s |
| 104 | 1.8 / 3.8 s | 2,718 of 12,752 | 506 (4.0%) | 39.1 (40.9) | 870/921 (94.5%) | 98.3% | 12.9 / 18.4 s |
| 112 | 2.2 / 4.0 s | 5,753 of 13,737 | 2,410 (17.5%) | 35.4 (44.1) | 934/992 (94.2%) | 98.7% | 13.9 / 20.4 s |

  **One laptop now keeps up with 96 stations** with every window answered (lag p99 2.4 s, under the 2.5 s hop, as 88 was before), where D50's worker dropped half of them. Past that it degrades gently instead of collapsing: at 104 it drops 4% of windows and at 112 18%, yet still finds 94.5% and 94.2% of songs (85.7% and 58.5% before D51), as the decoder rides over a missing window, and reports them a median 12.9 and 13.9 s after their transitions. The worker answers up to 39.1 windows a second, at 104; at 112 it answers 35.4, a smaller dip than before (10.1). At 88 stations every stream's event log equals D50's (`rt_cap88_d50`), evidence counts included, as D51 changes no landmark, with lag p99 1.9 s against 2.1 s. The machine spent a median 11% of its CPU in the kernel at 96 stations (30 to 37% in D50's diagnostic run) with a median 11 GB still compressed: other applications' memory still squeezes the laptop, but the worker no longer churns through it.
- **Detection delay at real time** is the 12.5 s the decoder needs (D38) plus search time: median 12.6 s at 24 stations.
- **Failover** (`rt_kill16`): 16 stations on worker w0, a standby w1, w0 killed with SIGKILL 120 s into the broadcast. All 16 streams moved to w1 (16 leases expired, 32 grants in all); each lost 5.2 s of audio (the lease ran out 6 s after w0's last renewal, shortly before the kill, and w1 took the stream at its next heartbeat); the new holder's first event came 21 to 59 s after the kill (when its first song decision fell due). No sequence gaps. The handoff itself duplicated nothing: 15 of the 16 streams carried their song in progress across it (one entry, flagged resumed), and the 16th closed it as interrupted because another song was playing by then. The 2 repeats in the score are songs the decoder split into two entries within the new holder's run (gaps of 5 and 7.5 s, beyond the 2.5 s merge gap), as offline decoding also does. Songs found 137/143 (95.8%), precision 99.3%, window lag p99 1.3 s.

## B10: adding songs while serving (D42)

`bench.scripts.live_writes`: a clone of the fma_medium pair index (P=1) served by two replicas following its manifests, behind a coordinator, under 200 qps of test queries (open loop, 450 s measured). Ten batches of 100 never-indexed fma_medium tracks were added with `ingest -add` (4 workers) every 30 s, then merged with `ingest -merge`. Commit 61f245e, AC power, battery "100%; charged". Raw: `bench/results/livewrites_r200`.

| Phase | Queries | p50 | p99 | Errors |
|---|---|---|---|---|
| Baseline | 7,908 | 12.3 ms | 20.8 ms | 0 |
| During adds | 15,965 | 14.2 ms | 28.4 ms | 0 |
| Between adds | 59,859 | 13.7 ms | 22.5 ms | 0 |
| During the merge | 8,268 | 15.1 ms | 23.7 ms | 0 |

- **A batch of 100 songs is searchable about 3 s after the add starts**: published (fingerprinted, delta segment written, manifest published) in 2.5 to 2.7 s, and identified through the coordinator 0.3 to 0.5 s later (the shards poll every 0.5 s, then swap).
- **Adding costs queries little**: p99 28.4 ms while a batch is fingerprinted on 4 of the same 12 cores, against 20.8 ms at baseline; no errors in any phase, including both replicas swapping versions under load.
- **The merge** rewrote the partition's whole base (108.1M postings, `shard0.log`) in 36 s, with p99 23.7 ms meanwhile. Its cost grows with the partition, not the deltas; merging deltas with each other first, as LSM trees do, would keep it small.
- **Accuracy**: 10 s excerpts of the 1,000 added songs identified at 99.8%, against 99.6% for 1,000 songs indexed from the start (after the merge).
- **Stop-list drift** (D42 W3, frozen counts): postings scanned per search rose 2.4% (555k to 568k, and 553k to 566k on the two replicas) as the catalog grew 4%.

## B11: head-to-head cost (D41)

`python -m eval.h2h cost`: every system indexes the same first 2,000 references (`data/h2h/refs.tsv`), and answers 200 clean 10 s test queries on one core against its full index (24,287 tracks), each query decoded by the tool's own ffmpeg call. Build cost is compared in CPU seconds per track, since Panako runs on 4 threads (its native transform crashed under 12); query time is per clip, with each tool's database load or JVM start subtracted (measured as a one-query run). Settings as chosen on val (EVAL, head-to-head). AC power, battery "100%; charged". Raw: `eval/results/h2h/costs.json`.

| System | Build CPU per track | Index bytes per track | Query time per clip, one core |
|---|---|---|---|
| Landmark, pairs | 150 ms | 28.6 KB | 75 ms |
| Landmark, triplets | 188 ms | 64.1 KB | 179 ms |
| audfprint, density 80 | 169 ms | 9.5 KB | 84 ms |
| Panako, time filter 15 | 344 ms | 239 KB | 235 ms |

- Landmark's pair index is 3x audfprint's per track (it keeps every landmark; audfprint's fixed-size hash table dropped 25.7% of its hashes at density 80, `eval/results/h2h/index_sizes.txt`) and answers in about the same time; its triplet index is a quarter of Panako's per track and answers a query in three quarters of the time.
- Full-index sizes (`index_sizes.txt`, `index_costs.json`): Landmark pairs 393 MB, triplets 1.03 GB, audfprint (density 80) 161 MB, Panako 0.84 GB at its default and 3.05 GB at time filter 15.
- Landmark's query times include its coordinator and shard on the same host (gRPC on localhost), one matcher per shard.

## B12: the hybrid's fingerprinting per window (D36)

`bench/querycost` (B6's method: 100 fma_medium test positives, one 10 s window each, 4 sub-hop shifts, one core, each figure the median over windows of the fastest of 3 passes), now also timing the windows resampled for the hybrid's double- and half-time triplet searches (resampling included) and its whole per-window fingerprinting. Commit 27d2fe2, AC power, battery "100%; charged". Raw: `bench/results/b12_querycost_hybrid.json`.

| Per 10 s window, one core | Median |
|---|---|
| Triplets, the window as is (dual filterbank) | 124.0 ms |
| Triplets, resampled to double time (20 s of audio) | 260.5 ms |
| Triplets, resampled to half time (5 s) | 62.5 ms |
| Pairs | 31.2 ms |
| The hybrid's whole fingerprinting (all of the above, plus the keylock rescales) | 486.8 ms |

- With about 145 ms of search per window (B8's rates), fingerprinting is about three quarters of a live window's CPU; the double-time triplet fingerprint alone is over half. This is what limited a laptop to about 32 live stations (B9), and what `docs/design/throughput.md` proposes to cut; D44 (B13) took the first, output-identical step.

## B13: one FFT per frame for both query filterbanks (D44)

`bench/querycost` as in B12, run twice in one session: at 97e7330 (before) and at 6e4ff44 (D44), AC power, battery "100%; charged" both times. Raw: `bench/results/b13_querycost_before.json`, `b13_querycost_after.json`; CPU profiles of one extra, untimed pass of the hybrid's fingerprinting: `b13_profile_before.txt`, `b13_profile_after.txt` (and `.pprof`).

| Per 10 s window, one core, median | Before | D44 |
|---|---|---|
| Triplets, the window as is (dual filterbank) | 123.3 ms | 77.2 ms |
| Triplets, resampled to double time | 258.7 ms | 161.0 ms |
| Triplets, resampled to half time | 61.8 ms | 38.5 ms |
| Pairs (code unchanged) | 30.6 ms | 32.0 ms |
| The hybrid's whole fingerprinting | 481.5 ms | 310.3 ms |

- 1.55x less fingerprinting per window for the same landmarks (median 13,977.5 per triplet query in both runs; D44's test checks identity landmark for landmark). The index side is unchanged (48.0 and 48.1 ms per track). The before run is within 1% of B12 (486.8 ms); the unchanged pair code moved 5% between runs, a guide to run-to-run spread.
- Before, the triplet spectrogram was 83% of the profile (the FFT 46% with its call overhead, magnitudes 21%), computed twice per frame. After, the triplet spectrograms are 74% (FFT 36%, magnitudes 16%) and peak picking 10%. With about 145 ms of search per window (B12), fingerprinting drops from about three quarters of a live window's CPU to about two thirds. In real time, one laptop now keeps up with 48 stations instead of 32 (B9).

## B14: a shared, frame-aligned front end, measured before building it (throughput T1 (a))

`bench/frontend`: the first 10 fma_medium test positives back to back as one 300 s stream. Today: windows every 27,562 samples (2.5 s), each fingerprinted with the triplet query fingerprint as is, at double time, and at half time (each resampled per window). Shared: windows every 27,520 samples (2.4961 s, a multiple of 128, so every window's 4 sub-hop grids fall on one 64-sample grid of the stream at all three speeds); the stream resampled once per speed, its dual-filterbank spectrograms computed once on the 64-sample grid, and each window's spectrograms cut out of them, then peak picking and hashing per window as today. One core, fastest of 3 passes; per-hop figures are totals divided by the 117 hops. Commit 00ec42b, AC power, battery "100%; charged". Raw: `bench/results/b14_frontend.json`.

| Per window, one core | Today | Shared front end |
|---|---|---|
| Spectrograms (per hop for the shared front end) | | 58.4 ms |
| Resampling to double and half time (per hop) | | 1.1 ms |
| Peaks and hashes | | 51.8 ms (mean) |
| Triplet fingerprints, all three speeds | 278.2 ms (mean) | 111.4 ms |

- 2.5x less triplet fingerprinting per window. With the pair fingerprint unchanged (32 ms, B13), a window's fingerprinting would go from 310 ms to about 143 ms.
- **Same landmarks.** Every one of the 117 windows gets exactly the landmarks today's FingerprintQuery gives for that window, at all three speeds (5.73M landmarks compared). Resampling the stream instead of each window changes only a window's outer few samples (the resampling filter reaches at most 20 samples past an edge), where the spectrogram's Hann window is near zero. So the option changes where windows start, not what a window yields.
- Not built: this measures the computation in batch. A live worker would compute the same frames incrementally as audio arrives.

## B15: what delta segments cost a search (D42; throughput T2)

`bench.scripts.delta_cost`: the same 1,000 never-indexed fma_medium tracks added to clones of the fma_medium pair index (P=1, 24,287 tracks before) as 1, 10, or 40 delta segments, or as one delta merged into the base (0). All four layouts served at once, each by one shard (8 workers) following its manifests, behind a coordinator with the 4% stop list; loadgen offered each 400 qps of 10 s test queries for 60 s after a 10 s warmup, the layouts taking turns, 3 reps. Shard CPU per search from `ps` (warmup included). Commit f364839, AC power, battery "100%; charged" throughout. Raw: `bench/results/b15_delta_cost/`.

| Layout | Segments | Shard CPU per search | p50 | p99 | Answered |
|---|---|---|---|---|---|
| Merged | 1 | 12.3 ms | 12.7 ms | 21.7 ms | 100% |
| 1 delta | 2 | 12.8 ms | 13.3 ms | 22.0 ms | 100% |
| 10 deltas | 11 | 14.9 ms | 15.4 ms | 24.0 ms | 100% |
| 40 deltas | 41 | 20.5 ms | 113 ms | 125-131 ms | 96.2-98.9% |

(Medians of 3 reps; the reps agree within 0.2 ms of CPU.)

- Every layout scans the same postings (576k per search); the extra cost is per segment, 0.2 to 0.5 ms of CPU per search for each delta (2 to 4% of a merged search; 0.5 ms for the first, 0.2 ms each across 40): each query hash is looked up in every segment.
- At 40 deltas the shard needs 400 x 20.5 ms = 8.2 cores of search against its 8 workers, so it saturates: queues grow, p50 jumps to 113 ms, and 1 to 4% of queries are shed. Ten deltas cost 21% more CPU; forty, 67%.
- So the number of segments, not the postings in them, is what a merge policy must bound: size-tiered merging (T2 (a)) keeps a handful of deltas between rewrites of the base.

## B16: adding songs with size-tiered merging (D47)

B10's run again with `ingest -add` merging by D47's policy after each add (`bench.scripts.live_writes --tiered`): ten batches of 100 never-indexed tracks every 30 s under 200 qps, then `-merge`. Commit 1c9d98a, AC power, battery "100%; charged". Raw: `bench/results/livewrites_tiered_r200`.

| Phase | Queries | p50 | p99 | Errors |
|---|---|---|---|---|
| Baseline | 7,897 | 12.4 ms | 20.9 ms | 0 |
| During adds | 16,107 | 13.7 ms | 23.5 ms | 0 |
| Between adds | 59,336 | 13.4 ms | 22.0 ms | 0 |
| During the merge | 8,660 | 14.1 ms | 22.8 ms | 0 |

- The partition held at most 6 segments (base plus 5 deltas; the shards' swaps show 2, 3, 4, 5, then 2 after the fourth add's tier merge, and so on), against 11 after ten adds without tiering. Each add is still searchable about 3 s after it starts (2.9 to 3.2 s; an add whose tier merge runs right after it publishes in 3.1 s instead of 2.6).
- Accuracy on the 1,000 added songs 99.8% (99.6% for songs indexed from the start), no errors in any phase. The final `-merge` rewrote the base in 38 s, as in B10.

## B17: one live window's fingerprinting, keeping buffers (D51)

`BenchmarkLiveWindow` (`internal/hybrid`): a 60 s noise stream pushed in quarter seconds through the shared front end, as the worker does, with each of its 21 windows' triplet landmarks cut at all three speeds and its pair landmarks computed. One core for the fingerprinting (the garbage collector may use others). Run at b6f0d1e with the benchmark added (before) and at b6c7fb4 (D51), from binaries built beforehand, 8 rounds alternating before and after, 3 streams each, AC power, battery "100%; charged", nothing else busy. Raw: `bench/results/b17_live_window/` (`before.txt`, `after.txt`, `summary.json`, `conditions.txt`).

| Per window | Before | D51 |
|---|---|---|
| Time, median of 8 rounds | 141.9 ms | 127.2 ms |
| Memory allocated | 111.9 MB | 11.0 MB |
| Allocations | 34,128 | 660 |

- 10% less time on an idle machine: D51 took 0.893 to 0.900 of the time in every round, while both drifted up about 3% over the 2 minutes. Same landmarks (D51's tests, and the test sets' window rows byte for byte).
- The time saved here is the cost of fresh memory when there is plenty of it. Under memory pressure each fresh page also has to be found, often by compressing another, which is what the worker profile showed past capacity (B9, "Why throughput collapses past capacity"); D51 removes 90% of that churn.
- Noise, not music: the stream's landmark density differs from a mix's, so the absolute times are a guide; the before and after run the same samples.
