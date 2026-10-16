# Progress

## Current phase: 4 (measure)
Phases 0-3 are done. Phase 4 has B3, B4, B5, and quick B1 sweeps recorded; B2 and full-method B1 on fma_medium are next.

## Done
- 2026-10-14, Phase 0: repo, toolchain, CI, compose stack, FMA data verified and extracted.
- 2026-10-14, Phase 1: Python fingerprinter (D9, D10), sorted-array index and matcher (D11), duplicate-aware splits (D12), eval harness with Wilson CIs, threshold calibration (D13), duplicate-aware ratio test (D14), 4-shift queries (D15), golden vectors. Interview Q&A for fingerprinter + matcher logged.
- 2026-10-14, Phase 2: Go audio + fingerprint with exact parity to Python (D16-D18, max filter 3.6x faster after profiling); segment format v1 with mmap reader and fuzzing (D19-D21); Go ingest; Go matcher with exact parity (D22); shard server with bounded matcher pool and load shedding (D23); identify CLI. Done criterion: the test split through the Go stack matches Python on all 21,978 rows.
- 2026-10-14, Phase 3: shard Info RPC and index manifest; cluster config; coordinator with per-attempt timeouts (D24), outlier ejection (D25), replica identity checks (D26), mutation-tested; 2x2 Docker cluster; open-loop loadgen with failure injection; Grafana dashboard. Done criterion (B3, 3 reps): kill -9 of a replica at 100 qps kept 100% complete answers, 2-3 of 6,000 requests per run over 50 ms.
- 2026-10-14 to 16, Phase 4: bench harness with AC-power and clean-tree guards; profiling at saturation found vote sorting (D27, radix sort) and per-message protobuf overhead (D28, packed arrays); single-partition knee on fma_small 600 to 1,600 qps, p50 12.7 to 5.4 ms; B5 (3 reps); fma_medium downloaded, deduped, split, indexed at P=1/2/4/8 (102M postings), evaluated (clean 10 s 99.9%, SNR 0 dB 97.1%, FAR <= 0.1%); B4 at medium shows the reachable hash space ~80% used; quick B1 sweep on medium.

## Next
- B2 on fma_medium with the median-of-3 rule (D29), then full-method B1 (30 s warmup, 120 s measured, 3 reps) at fractions of each P's B2 rate.
- Sean: decide how to handle the hash-space ceiling before fma_large (direct table per D20 option B, wider hash, or accept linear growth); measure postings scanned per query vs catalog size first.
- Sean: multi-machine run for real throughput scaling (said "not now" to AWS on 2026-10-14).
- Phase 5 (HTTP gateway + demo page).
- Optional: shift-invariant peak selection (D9 follow-up, core change), u32 offsets / compressed keys (B4).

## Blockers
- None. Long runs need AC power and the lid open.

## Open questions
- fma_large needs ~186 GB (zip + extracted); ~75 GiB free. External disk or a cloud VM.
- Real-noise condition (MUSAN or Sean's recordings) not yet in the eval; white noise only.
- 12 interview questions pending in `docs/INTERVIEW_NOTES.md` (Sean deferred; answer before resume use).

## Gotchas
- Benchmarks: AC power only. One decode measured 3x slower on battery at 43% charge; a full B5 run on battery at 100% charge was within a few percent of AC, so the size of the effect varies. Run metadata records `power`, and the bench scripts refuse battery.
- Never commit while a benchmark runs: each point records HEAD when it finishes. Bench scripts refuse uncommitted code.
- `caffeinate -i` does not stop a closed MacBook on battery from sleeping; long runs need the lid open. A sleep mid-run freezes processes and inflates wall-clock timings (the fma_medium ingest timings are unusable).
- Never gate a commit on a pipe (`make test | tail`): the pipe hides the failure (5744ed0 pushed a failing test).
- zsh: no word splitting of `$var` commands; an unmatched glob aborts an `&&` chain (that is how a debug run got committed); `pgrep -f` matches its own wait loop.
- Doc edits by script: replacing "from X to end of file" deletes later sections (it removed B3/B5 from BENCHMARKS.md once). Replace bounded spans only.
- A `kill -9`'d container's address disappears, so requests to it hang instead of failing fast; per-attempt timeouts (D24) bound the damage.
- Compose: services sharing one image tag must not all `build:` it.
- FMA zips need 7zip (`7zz x`). MP3 at 11,025 Hz caps at 64 kbps; degradations encode at 44.1 kHz.
- Re-run `eval.dedupe` whenever fingerprint params change.
