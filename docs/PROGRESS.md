# Progress

## Current phase: 4 (measure); Phase 3 done except scale-up to fma_medium/large

## Done
- 2026-10-14: Phase 0. Repo, toolchain, CI green, compose stack, fma_metadata + fma_small downloaded, SHA1 verified, extracted (7zip; unzip fails on FMA), zips deleted.
- 2026-10-14: Phase 1. Fingerprinter (D9, D10), sorted-array index and vectorized matcher (D11), duplicate-aware splits (D12), eval harness with 11 conditions and Wilson CIs, threshold calibration (D13). Results in `docs/EVAL.md`. Golden vectors for 20 tracks in `testdata/golden/`.
- 2026-10-14: D14 (duplicate-aware ratio test) and D15 (4-shift queries) implemented and measured; interview Q&A for fingerprinter + matcher logged.
- 2026-10-14 (Phase 2): D1 revisited and kept. Go `internal/audio` and `internal/fingerprint` with exact parity to Python (0 differing landmarks on 20 golden tracks; synthetic parity runs in CI). Python switched to float64 (D17), no test outcome changed. Max filter sped up 3.6x after profiling (D18). Throughput benchmark committed.
- 2026-10-14 (Phase 2): Segment format v1 (D19-D21): atomic writer, mmap reader, fuzzed (47.5M local execs, 20 s in CI). Go `cmd/ingest` builds per-partition segments; posting count matches Python exactly. B4 on fma_small recorded.
- 2026-10-14 (Phase 2): Go matcher with exact Python parity (D22). Shard server with fixed matcher pool and load shedding (D23), health, metrics, pprof. identify CLI: first end-to-end identification over gRPC on fma_small.

- 2026-10-14 (Phase 2 done): eval harness drives the live Go shard over gRPC (`--shard`); test split through the Go stack is identical to Python on all 21,978 rows.
- 2026-10-14 (Phase 3): shard Info RPC + index manifest; cluster config; coordinator with per-attempt timeouts (D24), outlier ejection (D25), identity verification (D26), mutation-tested; Docker cluster (2x2); open-loop loadgen with failure injection; B3 script. Battery smoke run: kill -9 of a replica at 50 qps kept 100% success, one slow request (~115 ms).

## Next
- 2026-10-14 (Phase 3 done criterion): official B3, 3 reps: replica kill -9 at 100 qps kept 100% complete answers; 2-3 of 6,000 requests per run over 50 ms (~106 ms), none after ejection.
- 2026-10-14 (Phase 4): bench harness; quick B1 sweeps; profiling found pdqsort at 54% of shard CPU at saturation; radix sort (D27) roughly doubled the knee; B5 (3 reps, AC); Grafana dashboard.
- 2026-10-14 (Phase 4): packed request format (D28), 40% smaller requests; quick sweep: P=1 knee 1,600 qps, p50 5.4 ms. fma_medium downloaded, verified, extracted; fma_small replaced by a symlink after a byte-for-byte check.
- Next: `bench/scripts/medium_pipeline.sh` (dedupe, splits, ingest P=1,2,4,8, eval at scale, queries), then full-method B1/B2 on fma_medium.
- Cloud multi-node run for real B2 scaling: options and cost for Sean.
- Scale up: fma_medium (22 GiB zip, needs Sean's OK), then fma_large (needs ~186 GB: external disk or cloud VM).
- Decide on per-key overhead (u32 offsets, compressed keys); see B4 notes.
- Optional: make D9 peak selection shift-invariant (rank within a window centered on each peak). Core change, needs Sean's pick.

## Blockers
- None.

## Open questions
- fma_large needs ~186 GB (zip + extracted); ~113 GiB free. External SSD or cloud VM for Phase 3-4 at scale. Decide before Phase 3.
- Real-noise condition (MUSAN or Sean's recordings) not yet in the eval; white noise only.
- Some fma_small clips are shorter than the 10 s excerpt; one track per split was skipped. Count them before Phase 4 eval.

## Gotchas
- A `kill -9`'d container's IP disappears, so in-flight requests to it hang instead of failing fast; per-attempt timeouts (D24) are what bound the damage.
- Compose: several services sharing one image tag must not all `build:` it (parallel builds collide); one builds, the rest `pull_policy: never`.
- Benchmarks on battery are throttled ~3x on this laptop (single decode 0.33 s vs 0.10 s). Run metadata now records `power`; only use `ac` runs for timing claims.
- Never gate a commit on `make test | tail`: the pipe returns tail's status, so failures slip through (it happened once: 5744ed0 pushed a failing test). Run `make test && make lint && git commit`, with output to a file if it must be short.
- zsh: an unmatched glob is an error and stops an `&&` chain (use `rm -f -- path` with exact names, or `setopt null_glob`).
- Never commit while a benchmark runs: each point records HEAD when it finishes, so a mid-run commit splits one sweep across commits. Bench scripts refuse dirty trees and battery power.
- `pgrep -f` inside a wait loop matches the loop's own command line; wait on a PID or a file instead.
- FMA zips need 7zip (`7zz x`), not unzip.
- MP3 at 11,025 Hz caps at 64 kbps; degradations encode at 44.1 kHz.
- Re-run `eval.dedupe` whenever fingerprint params change: duplicate clusters depend on the fingerprinter.
