# Progress

## Current phase: 2 (Go core, single node): audio + fingerprint done

## Done
- 2026-10-14: Phase 0. Repo, toolchain, CI green, compose stack, fma_metadata + fma_small downloaded, SHA1 verified, extracted (7zip; unzip fails on FMA), zips deleted.
- 2026-10-14: Phase 1. Fingerprinter (D9, D10), sorted-array index and vectorized matcher (D11), duplicate-aware splits (D12), eval harness with 11 conditions and Wilson CIs, threshold calibration (D13). Results in `docs/EVAL.md`. Golden vectors for 20 tracks in `testdata/golden/`.
- 2026-10-14: D14 (duplicate-aware ratio test) and D15 (4-shift queries) implemented and measured; interview Q&A for fingerprinter + matcher logged.
- 2026-10-14 (Phase 2): D1 revisited and kept. Go `internal/audio` and `internal/fingerprint` with exact parity to Python (0 differing landmarks on 20 golden tracks; synthetic parity runs in CI). Python switched to float64 (D17), no test outcome changed. Max filter sped up 3.6x after profiling (D18). Throughput benchmark committed.

## Next
- Segment format v1 (core: options for Sean first): writer, mmap reader, varint codec, round-trip property tests, fuzzing.
- Then `internal/match`, `cmd/shard` serving one segment, `cmd/identify` CLI.
- Optional: make D9 peak selection shift-invariant (rank within a window centered on each peak). Core change, needs Sean's pick.

## Blockers
- None.

## Open questions
- fma_large needs ~186 GB (zip + extracted); ~113 GiB free. External SSD or cloud VM for Phase 3-4 at scale. Decide before Phase 3.
- Real-noise condition (MUSAN or Sean's recordings) not yet in the eval; white noise only.
- Some fma_small clips are shorter than the 10 s excerpt; one track per split was skipped. Count them before Phase 4 eval.

## Gotchas
- FMA zips need 7zip (`7zz x`), not unzip.
- MP3 at 11,025 Hz caps at 64 kbps; degradations encode at 44.1 kHz.
- Re-run `eval.dedupe` whenever fingerprint params change: duplicate clusters depend on the fingerprinter.
