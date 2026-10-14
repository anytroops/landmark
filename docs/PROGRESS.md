# Progress

## Current phase: 1 (Python prototype), core done; open decisions below

## Done
- 2026-10-14: Phase 0. Repo, toolchain, CI green, compose stack, fma_metadata + fma_small downloaded, SHA1 verified, extracted (7zip; unzip fails on FMA), zips deleted.
- 2026-10-14: Phase 1. Fingerprinter (D9, D10), sorted-array index and vectorized matcher (D11), duplicate-aware splits (D12), eval harness with 11 conditions and Wilson CIs, threshold calibration (D13). Results in `docs/EVAL.md`. Golden vectors for 20 tracks in `testdata/golden/`.

## Next
- Sean: pick the acceptance-rule fix for duplicates, and whether to address sub-hop misalignment now or after the Go port.
- Sean: answer the 3 interview questions on fingerprinter + matcher (log in INTERVIEW_NOTES.md).
- Phase 2: Go `internal/audio` + `internal/fingerprint`, parity test against `testdata/golden/` (Jaccard >= 0.98).

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
