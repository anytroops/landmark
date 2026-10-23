# Progress

## Current phase: roadmap complete; next direction open
Sean's 2026-10-21 roadmap is done: live stream monitoring (D38-D40), the head-to-head against audfprint and Panako (D41), adding songs while serving (D42), and DJ mix analysis (D43), each built, measured, and written up. The demo UI waits for Sean's go; product direction to be discussed.

## Done
- 2026-10-14, Phase 0: repo, toolchain, CI, compose stack, FMA data verified and extracted.
- 2026-10-14, Phase 1: Python fingerprinter (D9, D10), sorted-array index and matcher (D11), duplicate-aware splits (D12), eval harness with Wilson CIs, threshold calibration (D13), duplicate-aware ratio test (D14), 4-shift queries (D15), golden vectors. Interview Q&A for fingerprinter + matcher logged.
- 2026-10-14, Phase 2: Go audio + fingerprint with exact parity to Python (D16-D18, max filter 3.6x faster after profiling); segment format v1 with mmap reader and fuzzing (D19-D21); Go ingest; Go matcher with exact parity (D22); shard server with bounded matcher pool and load shedding (D23); identify CLI. Done criterion: the test split through the Go stack matches Python on all 21,978 rows.
- 2026-10-14, Phase 3: shard Info RPC and index manifest; cluster config; coordinator with per-attempt timeouts (D24), outlier ejection (D25), replica identity checks (D26), mutation-tested; 2x2 Docker cluster; open-loop loadgen with failure injection; Grafana dashboard. Done criterion (B3, 3 reps): kill -9 of a replica at 100 qps kept 100% complete answers, 2-3 of 6,000 requests per run over 50 ms.
- 2026-10-14 to 16, Phase 4: bench harness with AC-power and clean-tree guards; profiling at saturation found vote sorting (D27, radix sort) and per-message protobuf overhead (D28, packed arrays); single-partition knee on fma_small 600 to 1,600 qps, p50 12.7 to 5.4 ms; B5 (3 reps); fma_medium downloaded, deduped, split, indexed at P=1/2/4/8 (102M postings), evaluated (clean 10 s 99.9%, SNR 0 dB 97.1%, FAR <= 0.1%); B4 at medium shows the reachable hash space ~80% used; quick B1 sweep on medium; B2 on medium (median-of-3 rule, D29): P=1/2/4/8 sustain 550/472/464/374 qps under p99 <= 100 ms on one host (P=1 about 520-550 by session); full-method B1 done (P=8 cuts p50 4.3x and p99 4.9x vs P=1 at similar load). Stop list (D30): P=1 925 qps at 2% (1.7x), clean accuracy unchanged, heavy-noise accuracy down 2.7 points; P=1 and P=8 answer identically (bench/equiv). Found that charging the battery during runs likely cuts capacity enough to fail a knee-rate (harness now waits for full charge). Measured postings scanned per query: linear in catalog size at any key-space fill, concentrated in a few long lists of held-note and low-frequency hashes.

- 2026-10-17, DJ tracklisting so far: speed tolerance of the pair fingerprint (turntable +/-0.75%; keylock barely hurts once simulated with WSOLA instead of librosa's phase vocoder, which smears peaks); synthetic DJ mixes with exact ground truth (eval.mixgen, clean and with EQ, bass swap, noise, MP3); option A speed grid (eval.tracklist) and decoders (eval.tracklist_score): recall 67-74% to 99.4% on clean test mixes, 54-63% to 98.8-100% on hard ones; UnmixDB set222 (real club tracks with effects, built by others): 81% to 98%, turntable 51% to 96%. Front-end experiment chose a log filterbank over STFT 4096 for B2 (D33). B2 Python reference (prototype/b2.py, b2_match.py) recovers scale within 0.1% in one lookup. UnmixDB, all six sets sampled (528 mixes, fma_medium distractors, calibrated on two sets, tested on four): 82.8% to 95.2% recall, turntable 57% to 89%; the line-fit decoder transfers once calibrated on UnmixDB (93.3%, precision 99.7%). B2 vs pairs on 3,000 tracks: one search covers turntable and keylock +/-1-8% at 97.7-99.9% (pairs: 0% past +/-2%), speed error 0.02-0.06 points; costs 95.7% vs 98.5% at SNR 0 dB and 2.2x postings; turntable scores dip at half-band pitch shifts. Hybrid B2 + pairs (D36) on UnmixDB: 95.9% of songs at 97.9% precision with 6 searches per window (grid 95.2% with 37).

- 2026-10-18 to 20, DJ tracklisting finished: dual-filterbank query (D35, turntable dips gone), hybrid matcher in eval and `cmd/tracklist` (D36; UnmixDB 95.2-95.9% of songs, CLI reproduces the eval on set044), pruned B2 voting (D37; 118 to 378 qps under p99 <= 100 ms at P=1). 11 GB of unused data removed.

- 2026-10-21, live monitoring built (D38-D40): online decoder (equal to offline, unit-tested), shared hybrid window search, streaming audio, registry (leases, epochs, fenced appends, event logs, subscriptions), worker, test stream server, harness and scorer. Live output equals `cmd/tracklist` and the eval decode on all 96 set044 mixes (268/285 songs, precision 97.8%); stream-time detection delay median 12.5 s.

- 2026-10-21 to 22: head-to-head harness (D41; audfprint and Panako installed, indexed, clip queries answered by all four systems); adding songs while serving built (D42: segment sets, manifests, shard version swaps, version-pinned coordinator, ingest -add/-merge, catalog per version, load harness); D43 picked.

- 2026-10-22: timing runs (live capacity 32 stations, failover, songs added under load, head-to-head cost); head-to-head written up and its tools removed; DJ analysis finished (BPM within 4% for 95.1% of test songs; Go port `tracklist -analyze` matches the prototype on all 1,058 test songs).

- 2026-10-22 evening: hybrid fingerprinting measured at 487 ms of CPU per window, about three quarters of live monitoring's work (B12); options drafted for more streams per machine and cheaper merges (`docs/design/throughput.md`, T1 and T2); DJ analysis rescored against every fade UnmixDB labels (median fade-edge errors 0.30-1.83 s); `cmd/events` (follow live monitoring as it happens) and `bench/scripts/live_demo.py` (one command: clusters, registry, stations, worker, events).

- 2026-10-22 night: profiled the per-window fingerprinting (83% in the triplet spectrogram, its FFT done twice by the dual filterbank); D44 computes each FFT once for both filterbanks, output identical: fingerprinting 481.5 to 310.3 ms per window (B13), real-time capacity 32 to 48 stations, saturated at 56 (B9). D45: a live source that drops is joined again and the song in progress continues (before, the worker idled on the stream for good); 16 real-time stations log the same events as before. Measured throughput option T1 (a) before building it (B14): 2.5x less triplet fingerprinting per window with every window's landmarks unchanged.

## Next
- Demo UI (on Sean's go): on top of live monitoring and `tracklist -analyze`.
- Sean's picks on `docs/design/throughput.md`: T1 (more live streams per machine; recommended a shared, frame-aligned front end, now measured: B14) and T2 (merging added songs; recommended size-tiered merges).
- Sean to review D44 and D45, made while he was away: D44 is output-identical (like D18 and D27); D45 changes how a worker handles a dropped live connection.
- When the DJ work is retired: delete fma_medium, UnmixDB, the medium indexes, data/queries.

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
