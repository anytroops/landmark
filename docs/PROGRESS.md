# Progress

## Current phase: 2 (Go core, single node): nearly done

## Done
- 2026-10-14: Phase 0. Repo, toolchain, CI green, compose stack, fma_metadata + fma_small downloaded, SHA1 verified, extracted (7zip; unzip fails on FMA), zips deleted.
- 2026-10-14: Phase 1. Fingerprinter (D9, D10), sorted-array index and vectorized matcher (D11), duplicate-aware splits (D12), eval harness with 11 conditions and Wilson CIs, threshold calibration (D13). Results in `docs/EVAL.md`. Golden vectors for 20 tracks in `testdata/golden/`.
- 2026-10-14: D14 (duplicate-aware ratio test) and D15 (4-shift queries) implemented and measured; interview Q&A for fingerprinter + matcher logged.
- 2026-10-14 (Phase 2): D1 revisited and kept. Go `internal/audio` and `internal/fingerprint` with exact parity to Python (0 differing landmarks on 20 golden tracks; synthetic parity runs in CI). Python switched to float64 (D17), no test outcome changed. Max filter sped up 3.6x after profiling (D18). Throughput benchmark committed.
- 2026-10-14 (Phase 2): Segment format v1 (D19-D21): atomic writer, mmap reader, fuzzed (47.5M local execs, 20 s in CI). Go `cmd/ingest` builds per-partition segments; posting count matches Python exactly. B4 on fma_small recorded.
- 2026-10-14 (Phase 2): Go matcher with exact Python parity (D22). Shard server with fixed matcher pool and load shedding (D23), health, metrics, pprof. identify CLI: first end-to-end identification over gRPC on fma_small.

## Next
- Phase 2 done criterion: eval through the Go shard matching Python accuracy. Plan: add a `--shard host:port` backend to `eval/run_eval.py` (Python gRPC stubs via buf) so the same harness drives the live system; reuse it for the coordinator in Phase 3.
- Benchmark matcher and shard latency once on AC power (pprof first, per D22).
- Phase 3 (coordinator, replication): suggest xhigh effort for the fan-out/failover work.
- Decide on per-key overhead (u32 offsets, compressed keys); see B4 notes.
- Optional: make D9 peak selection shift-invariant (rank within a window centered on each peak). Core change, needs Sean's pick.

## Blockers
- None.

## Open questions
- fma_large needs ~186 GB (zip + extracted); ~113 GiB free. External SSD or cloud VM for Phase 3-4 at scale. Decide before Phase 3.
- Real-noise condition (MUSAN or Sean's recordings) not yet in the eval; white noise only.
- Some fma_small clips are shorter than the 10 s excerpt; one track per split was skipped. Count them before Phase 4 eval.

## Gotchas
- Benchmarks on battery are throttled ~3x on this laptop (single decode 0.33 s vs 0.10 s). Run metadata now records `power`; only use `ac` runs for timing claims.
- `pgrep -f` inside a wait loop matches the loop's own command line; wait on a PID or a file instead.
- FMA zips need 7zip (`7zz x`), not unzip.
- MP3 at 11,025 Hz caps at 64 kbps; degradations encode at 44.1 kHz.
- Re-run `eval.dedupe` whenever fingerprint params change: duplicate clusters depend on the fingerprinter.
