# Progress

## Current phase: 4 (measure)
Phases 0-3 are done. Phase 4 has B2, B3, B4, B5, and quick B1 sweeps recorded; full-method B1 on fma_medium is next.

## Done
- 2026-10-14, Phase 0: repo, toolchain, CI, compose stack, FMA data verified and extracted.
- 2026-10-14, Phase 1: Python fingerprinter (D9, D10), sorted-array index and matcher (D11), duplicate-aware splits (D12), eval harness with Wilson CIs, threshold calibration (D13), duplicate-aware ratio test (D14), 4-shift queries (D15), golden vectors. Interview Q&A for fingerprinter + matcher logged.
- 2026-10-14, Phase 2: Go audio + fingerprint with exact parity to Python (D16-D18, max filter 3.6x faster after profiling); segment format v1 with mmap reader and fuzzing (D19-D21); Go ingest; Go matcher with exact parity (D22); shard server with bounded matcher pool and load shedding (D23); identify CLI. Done criterion: the test split through the Go stack matches Python on all 21,978 rows.
- 2026-10-14, Phase 3: shard Info RPC and index manifest; cluster config; coordinator with per-attempt timeouts (D24), outlier ejection (D25), replica identity checks (D26), mutation-tested; 2x2 Docker cluster; open-loop loadgen with failure injection; Grafana dashboard. Done criterion (B3, 3 reps): kill -9 of a replica at 100 qps kept 100% complete answers, 2-3 of 6,000 requests per run over 50 ms.
- 2026-10-14 to 16, Phase 4: bench harness with AC-power and clean-tree guards; profiling at saturation found vote sorting (D27, radix sort) and per-message protobuf overhead (D28, packed arrays); single-partition knee on fma_small 600 to 1,600 qps, p50 12.7 to 5.4 ms; B5 (3 reps); fma_medium downloaded, deduped, split, indexed at P=1/2/4/8 (102M postings), evaluated (clean 10 s 99.9%, SNR 0 dB 97.1%, FAR <= 0.1%); B4 at medium shows the reachable hash space ~80% used; quick B1 sweep on medium; B2 on medium (median-of-3 rule, D29): P=1/2/4/8 sustain 550/472/464/374 qps under p99 <= 100 ms on one host (P=1 about 520-550 by session); full-method B1 done (P=8 cuts p50 4.3x and p99 4.9x vs P=1 at similar load). Stop list (D30): P=1 925 qps at 2% (1.7x), clean accuracy unchanged, heavy-noise accuracy down 2.7 points; P=1 and P=8 answer identically (bench/equiv). Found that charging the battery during runs likely cuts capacity enough to fail a knee-rate (harness now waits for full charge). Measured postings scanned per query: linear in catalog size at any key-space fill, concentrated in a few long lists of held-note and low-frequency hashes.

## Next
- DJ tracklisting (D31, D32, docs/design/dj-tracklisting.md): Mixotic's Google Drive links now require sign-in; Sean to fetch it or choose UnmixDB / synthetic FMA mixes. Delete Mixotic when the feature is done (Sean's condition).
- Before/after B2 at P=1 on fma_medium for the D27+D28 optimizations (pre-3b7d8d2 binaries), so the 2.7x claim has full-methodology numbers.
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
- Benchmarks: never while the battery charges. On the 65 W adapter (the original 70 W is lost), P=1 at 550 qps failed all reps while charging from 82%, after passing B2 at full charge. The harness waits for the charge to finish and invalidates runs where the battery drained or began charging.
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
