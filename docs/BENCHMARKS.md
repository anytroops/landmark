# Benchmarks

Methodology: see `docs/PROJECT_BRIEF.md` section 14. Every number here traces to a file in `bench/results/`.

| ID | Benchmark | Result |
|---|---|---|
| B1 | Latency p50/p95/p99/p99.9 vs offered QPS, per partition count | [TBD] |
| B2 | Max sustainable QPS under p99 SLO vs partitions | [TBD] |
| B3 | Failure-injection timeline around a replica kill | [TBD] |
| B4 | Index size: raw vs encoded, bytes per posting | [TBD] |
| B5 | Ingest throughput vs worker count | [TBD] |

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
