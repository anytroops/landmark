# Progress

## Current phase: DJ-set tracklisting (D31, D32, D33)
Phases 0-4 are done (Phase 4: B1-B5 on fma_medium, stop list D30, D27+D28 before/after). The current work is the differentiating feature: tracklisting DJ mixes with tempo-robust matching. Option A (speed grid) is built and evaluated; option B2 (tempo-invariant triplet hashes on a log-frequency front end) has a Python reference and is being compared with today's fingerprint, before the Go port.

## Done
- 2026-10-14, Phase 0: repo, toolchain, CI, compose stack, FMA data verified and extracted.
- 2026-10-14, Phase 1: Python fingerprinter (D9, D10), sorted-array index and matcher (D11), duplicate-aware splits (D12), eval harness with Wilson CIs, threshold calibration (D13), duplicate-aware ratio test (D14), 4-shift queries (D15), golden vectors. Interview Q&A for fingerprinter + matcher logged.
- 2026-10-14, Phase 2: Go audio + fingerprint with exact parity to Python (D16-D18, max filter 3.6x faster after profiling); segment format v1 with mmap reader and fuzzing (D19-D21); Go ingest; Go matcher with exact parity (D22); shard server with bounded matcher pool and load shedding (D23); identify CLI. Done criterion: the test split through the Go stack matches Python on all 21,978 rows.
- 2026-10-14, Phase 3: shard Info RPC and index manifest; cluster config; coordinator with per-attempt timeouts (D24), outlier ejection (D25), replica identity checks (D26), mutation-tested; 2x2 Docker cluster; open-loop loadgen with failure injection; Grafana dashboard. Done criterion (B3, 3 reps): kill -9 of a replica at 100 qps kept 100% complete answers, 2-3 of 6,000 requests per run over 50 ms.
- 2026-10-14 to 16, Phase 4: bench harness with AC-power and clean-tree guards; profiling at saturation found vote sorting (D27, radix sort) and per-message protobuf overhead (D28, packed arrays); single-partition knee on fma_small 600 to 1,600 qps, p50 12.7 to 5.4 ms; B5 (3 reps); fma_medium downloaded, deduped, split, indexed at P=1/2/4/8 (102M postings), evaluated (clean 10 s 99.9%, SNR 0 dB 97.1%, FAR <= 0.1%); B4 at medium shows the reachable hash space ~80% used; quick B1 sweep on medium; B2 on medium (median-of-3 rule, D29): P=1/2/4/8 sustain 550/472/464/374 qps under p99 <= 100 ms on one host (P=1 about 520-550 by session); full-method B1 done (P=8 cuts p50 4.3x and p99 4.9x vs P=1 at similar load). Stop list (D30): P=1 925 qps at 2% (1.7x), clean accuracy unchanged, heavy-noise accuracy down 2.7 points; P=1 and P=8 answer identically (bench/equiv). Found that charging the battery during runs likely cuts capacity enough to fail a knee-rate (harness now waits for full charge). Measured postings scanned per query: linear in catalog size at any key-space fill, concentrated in a few long lists of held-note and low-frequency hashes.

- 2026-10-17, DJ tracklisting so far: speed tolerance of the pair fingerprint (turntable +/-0.75%; keylock barely hurts once simulated with WSOLA instead of librosa's phase vocoder, which smears peaks); synthetic DJ mixes with exact ground truth (eval.mixgen, clean and with EQ, bass swap, noise, MP3); option A speed grid (eval.tracklist) and decoders (eval.tracklist_score): recall 67-74% to 99.4% on clean test mixes, 54-63% to 98.8-100% on hard ones; UnmixDB set222 (real club tracks with effects, built by others): 81% to 98%, turntable 51% to 96%. Front-end experiment chose a log filterbank over STFT 4096 for B2 (D33). B2 Python reference (prototype/b2.py, b2_match.py) recovers scale within 0.1% in one lookup. UnmixDB, all six sets sampled (528 mixes, fma_medium distractors, calibrated on two sets, tested on four): 82.8% to 95.2% recall, turntable 57% to 89%; the line-fit decoder transfers once calibrated on UnmixDB (93.3%, precision 99.7%). B2 vs pairs on 3,000 tracks: one search covers turntable and keylock +/-1-8% at 97.7-99.9% (pairs: 0% past +/-2%), speed error 0.02-0.06 points; costs 95.7% vs 98.5% at SNR 0 dB and 2.2x postings; turntable scores dip at half-band pitch shifts.

## Next
- B2 (D33): DJ-mix tracklisting with B2 (synthetic and UnmixDB, against the speed grid), then fix the half-band dips (fingerprint the query on a filterbank offset by half a band too), then cost (latency per window, B2 vs grid). Done: deterministic Python matcher, Go triplet fingerprint and matcher (bit-exact parity), segment v2 (D34), proto fields, shard dispatch by segment version, coordinator passthrough with the stop list, ingest -fingerprint triplets, Python client, eval.tracklist --fingerprint b2.
- Mixotic (the published DJ-mix benchmark) is access-restricted on Google Drive; using UnmixDB (built from the same tracks) instead. If Mixotic is ever obtained, delete it when the feature is done (Sean's condition).
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
