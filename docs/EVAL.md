# Evaluation

Methodology: `docs/PROJECT_BRIEF.md` section 13, splits per D12, thresholds per D13. Every number here traces to a file in `eval/results/`.

## Current: Python prototype, D14 + D15 (fma_small, seed 0, tag `_d14_shift4`)

Duplicate-aware ratio test (D14) and 4-shift query fingerprinting (D15). Thresholds from val: t_abs 30, t_ratio 1.0 (the ratio test no longer helps once queries carry more landmarks). Raw rows: `eval/results/queries_test_seed0_d14_shift4.csv.gz`; summary: `eval/results/summary_seed0_d14_shift4.json`.

| Condition | Top-1 raw (95% CI) | Accuracy at threshold (95% CI) | FAR (95% CI) | n pos / neg |
|---|---|---|---|---|
| clean_10s | 100.0 (99.6-100.0) | 100.0 (99.6-100.0) | 0.1 (0.0-0.6) | 1000 / 998 |
| clean_5s | 100.0 (99.6-100.0) | 100.0 (99.6-100.0) | 0.2 (0.1-0.7) | 1000 / 998 |
| mp3_128k_10s | 100.0 (99.6-100.0) | 100.0 (99.6-100.0) | 0.2 (0.1-0.7) | 1000 / 998 |
| mp3_64k_10s | 100.0 (99.6-100.0) | 100.0 (99.6-100.0) | 0.2 (0.1-0.7) | 1000 / 998 |
| speed_+2pct_10s | 66.1 (63.1-69.0) | 0.6 (0.3-1.3) | 0.0 (0.0-0.4) | 1000 / 998 |
| speed_-2pct_10s | 67.9 (64.9-70.7) | 0.3 (0.1-0.9) | 0.0 (0.0-0.4) | 1000 / 998 |
| white_snr0_10s | 99.6 (99.0-99.8) | 95.4 (93.9-96.5) | 0.0 (0.0-0.4) | 1000 / 998 |
| white_snr10_10s | 100.0 (99.6-100.0) | 99.9 (99.4-100.0) | 0.1 (0.0-0.6) | 1000 / 998 |
| white_snr15_10s | 100.0 (99.6-100.0) | 100.0 (99.6-100.0) | 0.1 (0.0-0.6) | 1000 / 998 |
| white_snr5_10s | 99.9 (99.4-100.0) | 98.8 (97.9-99.3) | 0.0 (0.0-0.4) | 1000 / 998 |
| white_snr5_5s | 99.4 (98.7-99.7) | 94.9 (93.4-96.1) | 0.0 (0.0-0.4) | 1000 / 998 |

### Ablation (accuracy at threshold, test split)

| Condition | Baseline | + D14 | + D14 + D15 |
|---|---|---|---|
| clean_10s | 99.2 | 100.0 | 100.0 |
| clean_5s | 99.1 | 100.0 | 100.0 |
| white_snr15_10s | 98.7 | 99.7 | 100.0 |
| white_snr10_10s | 98.5 | 99.5 | 99.9 |
| white_snr5_10s | 96.9 | 98.0 | 98.8 |
| white_snr0_10s | 93.5 | 94.5 | 95.4 |
| white_snr5_5s | 93.2 | 94.0 | 94.9 |
| mean search time per query (ms) | 7.3 | 6.9 | 13.8 |

Sources: `summary_seed0.json` (baseline), `summary_seed0_d14.json`, `summary_seed0_d14_shift4.json`; search times from `queries_test_seed0*.meta.json` (Python, M4 Pro, not a latency benchmark). Each column uses its own val-calibrated thresholds.

**D15 paired test** (`eval/results/compare_seed0_d14_vs_d14_shift4.md`, exact McNemar on identical queries): every noise condition moves the same direction, but only white_snr5_10s reaches p < 0.05 (9 vs 1 discordant, p = 0.02); SNR 0 is 21 vs 12 (p = 0.16). Read it as a consistent ~1 point gain at low SNR that n = 1,000 cannot pin down, for 2x search time.

## fma_medium: 3.4x the catalog (Phase 4)

Index: 23,985 tracks (fma_medium minus held-out negatives and undecodable files), served by the Go stack (`cmd/ingest` P=1 segment, `cmd/shard` over gRPC, D14 + D15 + D27 + D28). Duplicate-aware splits per D12 (520 clusters, 1,408 tracks; `eval/manifests/duplicates_medium.json`). Thresholds recalibrated on the medium val split (D13 objective): t_abs 24, t_ratio 3.0. Raw: `eval/results/queries_{val,test}_seed0_medium.csv.gz`; val at commit 107f231, test at 77e4631 (the only change between them is the eval backend's retry of transient gRPC errors; 1 call was retried). Both clean.

| Condition | Top-1 raw (95% CI) | Accuracy at threshold (95% CI) | FAR (95% CI) | n pos / neg |
|---|---|---|---|---|
| clean_10s | 99.9 (99.4-100.0) | 99.9 (99.4-100.0) | 0.1 (0.0-0.6) | 1000 / 1000 |
| clean_5s | 99.8 (99.3-99.9) | 99.8 (99.3-99.9) | 0.1 (0.0-0.6) | 1000 / 1000 |
| mp3_128k_10s | 99.9 (99.4-100.0) | 99.9 (99.4-100.0) | 0.1 (0.0-0.6) | 1000 / 1000 |
| mp3_64k_10s | 99.9 (99.4-100.0) | 99.9 (99.4-100.0) | 0.1 (0.0-0.6) | 1000 / 1000 |
| speed_+2pct_10s | 61.0 (57.9-64.0) | 0.6 (0.3-1.3) | 0.0 (0.0-0.4) | 1000 / 1000 |
| speed_-2pct_10s | 63.4 (60.4-66.3) | 0.7 (0.3-1.4) | 0.0 (0.0-0.4) | 1000 / 1000 |
| white_snr0_10s | 99.5 (98.8-99.8) | 97.1 (95.9-98.0) | 0.0 (0.0-0.4) | 1000 / 1000 |
| white_snr10_10s | 99.9 (99.4-100.0) | 99.6 (99.0-99.8) | 0.0 (0.0-0.4) | 1000 / 1000 |
| white_snr15_10s | 99.9 (99.4-100.0) | 99.6 (99.0-99.8) | 0.1 (0.0-0.6) | 1000 / 1000 |
| white_snr5_10s | 99.7 (99.1-99.9) | 99.4 (98.7-99.7) | 0.0 (0.0-0.4) | 1000 / 1000 |
| white_snr5_5s | 99.6 (99.0-99.8) | 96.7 (95.4-97.6) | 0.0 (0.0-0.4) | 1000 / 1000 |

Accuracy and false accepts hold at 3.4x the catalog. Do not read the differences from the fma_small table as improvements: thresholds and test tracks differ, and most intervals overlap.

## Stop list (D30): accuracy per cutoff, fma_medium (Phase 4)

Same queries as the fma_medium section (seed 0, 1,000 positives and 1,000 negatives per condition per split), fingerprinted once and searched through a Go P=1 shard at four cutoffs: off, and dropping query landmarks whose hash has more postings than 1%, 2%, or 4% of the catalog's 23,985 tracks (239, 479, 959 postings). Thresholds recalibrated per cutoff on val (D13, FAR <= 0.1%). The "off" run reproduces the committed `_medium` rows exactly (all 22,000 rows per split, every column). Commit 8d50412. Raw: `eval/results/queries_{val,test}_seed0_medium_d30*.csv.gz`, summaries `summary_seed0_medium_d30*.md`, paired tests `compare_seed0_medium_d30_vs_medium_d30_stop*.md`.

Test accuracy at threshold (median postings scanned per query in parentheses):

| Condition | Off | 4% | 2% | 1% |
|---|---|---|---|---|
| clean_10s | 99.9 (689k) | 99.9 (524k) | 99.9 (388k) | 99.9 (238k) |
| clean_5s | 99.8 | 99.8 | 99.8 | 99.8 |
| mp3_128k_10s | 99.9 | 99.9 | 99.9 | 99.9 |
| mp3_64k_10s | 99.9 | 99.9 | 99.9 | 99.9 |
| white_snr15_10s | 99.6 | 99.6 | 99.6 | 99.6 |
| white_snr10_10s | 99.6 | 99.6 | 99.6 | 99.5 |
| white_snr5_10s | 99.4 | 98.7 | 98.7 | 98.5 |
| white_snr5_5s | 96.7 | 96.1 | 95.5 | 94.2 |
| white_snr0_10s | 97.1 | 95.5 | 94.4 | 93.6 |
| Pooled test FAR (non-speed) | 0.056% | 0.044% | 0.022% | 0.067% |
| Thresholds (t_abs, t_ratio) | 24, 3.0 | 2, 4.0 | 2, 4.0 | 16, 3.0 |

- **Clean and compressed audio are unaffected at every cutoff**, while the postings each query scans fall by 24%, 44%, and 65% (clean 10 s, median).
- **Heavy noise pays for it.** At SNR 0 dB the losses are 1.6, 2.7, and 3.5 points (exact McNemar p = 1.5e-4, 1.5e-8, 5.8e-11; the discordant queries go almost entirely one way). Under heavy noise the peaks that survive are disproportionately the strong low-frequency and held-note ones, which are exactly the hashes the stop list removes.
- FAR stays within the 0.1% target at every cutoff.
- Speed conditions stay broken (under 1.2%) at every cutoff; that is D31's job.

## Speed tolerance of the current fingerprint (D31 step 1)

fma_medium test positives (1,000 per point), clean 10 s excerpts with a tempo change, from 0.25% to 8% either way: turntable (`speed_`, resampled, so tempo and pitch move together), keylock by WSOLA (`keylock_`, time-domain overlap-add, tempo only, the family SoundTouch belongs to), and keylock by librosa's phase vocoder (`stretch_`). Go P=1 shard, stop list off, fma_medium thresholds (t_abs 24, t_ratio 3.0). Commits 5177a69 (turntable, phase vocoder) and 377644e (WSOLA). Raw: `eval/results/queries_test_seed0_medium_speedtol*.csv.gz`; tables `speedtol_seed0_medium_speedtol*.md`; figures `docs/figures/speed_tolerance_medium*.png` (the `_stop0.04` variants use the 4% stop list and its thresholds).

Accuracy at threshold / raw top-1 (%):

| Tempo change | -8% | -4% | -2% | -1% | -0.5% | +0.5% | +1% | +2% | +4% | +8% |
|---|---|---|---|---|---|---|---|---|---|---|
| Turntable, at threshold | 0.0 | 0.0 | 0.7 | 86.7 | 99.9 | 99.9 | 86.8 | 0.6 | 0.0 | 0.0 |
| Turntable, raw top-1 | 0.0 | 1.5 | 63.4 | 99.3 | 99.9 | 99.9 | 99.9 | 61.0 | 1.9 | 0.1 |
| Keylock (WSOLA), at threshold | 95.8 | 99.5 | 99.9 | 99.9 | 99.9 | 99.9 | 99.9 | 99.8 | 99.5 | 95.6 |
| Keylock (WSOLA), raw top-1 | 99.9 | 99.9 | 99.9 | 99.9 | 99.9 | 99.9 | 99.9 | 99.9 | 99.9 | 99.8 |
| Keylock (phase vocoder), at threshold | 2.7 | 27.6 | 84.1 | 99.3 | 99.8 | 99.9 | 99.9 | 99.8 | 97.8 | 40.5 |
| Keylock (phase vocoder), raw top-1 | 91.7 | 99.3 | 99.9 | 99.9 | 99.9 | 99.9 | 99.9 | 99.9 | 99.9 | 98.6 |

- **Turntable changes break the hashes.** Tolerance is about +/-0.75% (98.6% or better at threshold), and nothing survives +/-2%: a 1% change moves bin 250 by 2.5 bins, so (f1, f2) pairs stop matching. For D31 option A, a grid in 1% steps (at most 0.5% from the truth) keeps every query in the 99.9% zone; +/-8% takes 17 grid points.
- **Keylock barely hurts, once it is simulated realistically.** With WSOLA, today's fingerprint stays at 99% or better at threshold out to +/-6% and about 96% at +/-8%, with no correction, symmetric in direction: frequencies do not move and WSOLA copies waveform pieces intact, so most hashes survive; the query-to-reference offset still drifts (median clean score kept 9% at 4%), but the right track's peak stays far above the runner-up. The real obstacle for DJ mixes is therefore turntable-style changes.
- **The phase-vocoder rows are an artifact of that stretcher, not of key lock.** An earlier version of this section concluded that keylock "breaks the vote" from those rows. librosa's phase vocoder accumulates phase errors along its input, smearing the spectral peaks a few seconds in (on one track at +1.8%: 20% of the clean score kept in the first 10 s, 2% from 3.3 s in), and it degrades slowing down much more than speeding up (median clean score kept 16% at -0.25% vs 85% at +0.25%). WSOLA shows neither effect, so the WSOLA rows are the keylock model used from here on (eval.degrade.keylock).
- With the stop list at 4%, the same pattern holds with a slightly narrower tolerance (turntable at +/-1%: 76% instead of 87%; WSOLA keylock at +/-8%: 88 to 91% instead of 96%).

## DJ-mix tracklisting on synthetic mixes (D31 option A, D32)

Mixes from `eval.mixgen` (ground truth in `eval/mixes/`): 10 calibration mixes from val tracks (`val_mixed8`, 100 segments, 81 indexed, 30 min) and 20 test mixes from test tracks (`test_mixed8`, 200 segments, 166 indexed, 59 min). Each segment is 18 to 26 s of a 30 s clip, equal-power crossfades of 3 to 8 s, tempo change 0 for 20% of segments and otherwise uniform in +/-8%, half turntable and half keylock (WSOLA); 15% of segments are never-indexed tracks that must not be reported. Clean audio: no noise, EQ, or effects. `eval.tracklist` searches 10 s windows every 2.5 s through a Go P=1 shard with the stop list at 4%, under 33 hypotheses (unchanged, plus turntable and keylock at +/-1% to +/-8% in 1% steps); turntable is undone on the audio, keylock on the landmarks. "Today's system" uses only the unchanged hypothesis. `eval.tracklist_score` decodes with smoothing (D32 option i) or per-track line fits plus Viterbi (options ii + iii); each decoder's parameters were chosen by F1 on the calibration mixes, then frozen for the test mixes. An oracle check (`eval.tracklist oracle`) first confirmed the ground truth: every known smoke-mix segment, undone at its true speed, matched at the predicted offset (within 40 ms). Commit 060bace. Raw: `eval/results/tracklist/`.

Test mixes:

| Method | Songs found (recall) | Entries / correct | Precision | Start within 10 s | Median start error | Median speed error |
|---|---|---|---|---|---|---|
| Today's system, smoothing | 122 / 166 (73.5%) | 128 / 125 | 97.7% | 97% | 5.1 s | 1.40 points |
| Today's system, line fits + Viterbi | 111 / 166 (66.9%) | 112 / 112 | 100% | 97% | 5.5 s | 0.27 points |
| Speed grid, smoothing | 165 / 166 (99.4%) | 167 / 166 | 99.4% | 100% | 4.0 s | 0.20 points |
| Speed grid, line fits + Viterbi | 165 / 166 (99.4%) | 167 / 167 | 100% | 100% | 4.1 s | 0.06 points |

- **The speed grid recovers the tracks today's system misses**: recall 67 to 74% becomes 99.4%, with no false entries from the line-fit decoder. Without it, most turntable segments past about +/-1% go unrecognized (speed tolerance section).
- **Line fits estimate the tempo change to 0.06 percentage points** (median), against 0.2 for the grid's own 1% steps, because the slope of reference time against mix time is the speed.
- Start errors of about 4 s reflect the 2.5 s window hop and transitions placed at the crossfade midpoint; all are within 10 s.
- **These mixes are easy**: clean audio, short 30 s source clips, and no EQ, filtering, reverb, or crowd noise. Both decoders are near-perfect on them, so they do not separate the methods. The cost is 33 searches per window instead of 1.

**Harder mixes** (`val_hard8`, 10 mixes, 88 indexed segments; `test_hard8`, 20 mixes, 168 indexed segments): the same recipe plus random shelf EQ within +/-6 dB per segment (200 Hz and 3 kHz), bass-swap high-passing (250 Hz) of every incoming fade-in, pink noise at a per-mix SNR drawn from 5 to 15 dB (a stand-in for room and crowd noise; no recorded crowd), and an MP3 128 kbps round trip of the finished mix. Same pipeline and procedure (calibrate on val, freeze, score test), commit da43fa6.

| Method, hard test mixes | Songs found (recall) | Entries / correct | Precision | Start within 10 s | Median speed error |
|---|---|---|---|---|---|
| Today's system, smoothing | 106 / 168 (63.1%) | 107 / 106 | 99.1% | 98% | 1.35 points |
| Today's system, line fits + Viterbi | 91 / 168 (54.2%) | 92 / 92 | 100% | 99% | 0.24 points |
| Speed grid, smoothing | 168 / 168 (100%) | 169 / 169 | 100% | 99% | 0.20 points |
| Speed grid, line fits + Viterbi | 166 / 168 (98.8%) | 168 / 168 | 100% | 100% | 0.07 points |

- The booth degradations cost today's system 4 to 13 more points of recall than the clean mixes, and cost the speed grid nothing measurable: the fingerprint already tolerates noise, EQ, and MP3 (the conditions table above), and what defeats it is turntable tempo change.
- Still no separation between the two decoders; external mixes (UnmixDB, next) and cost are the remaining tests.

## DJ-mix tracklisting on UnmixDB, first set (preliminary)

UnmixDB (Schwarz and Fourer, ISMIR 2018 late-breaking; zenodo.org/records/1422385, CC BY-NC-ND 4.0) is a public set of DJ mixes built by other researchers from the Creative Commons dance tracks of the Mixotic set (the ISMIR 2016 DJ-mix benchmark, whose own files are no longer publicly downloadable). Each mix chains 3 reference excerpts beat-synchronously with linear crossfades, in 12 versions: tempo unchanged ("none"), turntable ("resample"), or keylock ("stretch", sox's tempo effect), times 4 effects (none, bass, compressor, distortion). Some tempo factors are about 2 (double-time matching), so the grid gains x2 and x0.5 hypotheses (37 in all). This is the first of six sets (`set222`: 13 reference excerpts, 48 mixes, 141 scored segments); the rest are still downloading. `eval.unmixdb` converts its labels to our ground-truth format; 15 of the 48 published mix files are truncated by more than 5 s (e.g. 67 s of audio for 112 s of labels), so segments are clipped to the audio present and 3 left with under 10 s are ignored. Oracle check: 131/141 segments matched at the labeled offset within 0.25 s; most misses are one excerpt consistently about 1 s off and double-time keylock segments with low scores. The index holds only the 13 reference excerpts (fma_medium as distractors comes with the full set). Decoder settings are the hard-mix calibration, frozen: UnmixDB played no part in choosing them. Commit 75ae4e3. Raw: `eval/results/tracklist/*unmixdb_set222*`.

| Tempo variant (segments) | Today's system, smoothing | Speed grid, smoothing | Speed grid, line fits + Viterbi |
|---|---|---|---|
| none (48) | 48 found, precision 100% | 48, 98.0% | 48, 100% |
| resample, turntable (45) | 23 (51%), 85.2% | 43 (96%), 97.7% | 38 (84%), 100% |
| stretch, keylock (48) | 43 (90%), 100% | 47 (98%), 97.9% | 40 (83%), 100% |
| All (141) | 114 (80.9%), 96.6% | 138 (97.9%), 97.9% | 126 (89.4%), 100% |

- **The synthetic result carries over to mixes built by others from real club tracks with effects**: today's system finds half the turntable-shifted songs, the speed grid 96%. Keylock costs little even without it (90%), as the WSOLA tolerance curve predicted.
- **The line-fit decoder's calibration does not transfer.** Tuned on 20 s clean synthetic segments, its low switch penalty lets it drop to "unknown" inside UnmixDB's 40 s effected segments and come back, splitting songs (181 entries for 126 found). Smoothing transfers. The fair fix is to calibrate on UnmixDB sets and test on others; the next section does.

## DJ-mix tracklisting on UnmixDB, all six sets (sampled)

All six UnmixDB sets, 8 of each set's playlists sampled (seed 0) with all 12 tempo/effect versions of each: 528 mixes, 1,584 segments, 302 reference excerpts. The references are indexed after fma_medium, so 23,985 real tracks act as distractors (one partition, stop list 4%). Search is the speed grid with the x2 and x0.5 hypotheses (37 per window, 20,213 windows). Decoder settings are calibrated on set222 + set275 (144 mixes, 425 scored segments) and tested on set044, set123, set230, set281 (384 mixes, 1,135 scored segments; 17 segments under 10 s after truncation are ignored). "No speed grid" is the same windows searched only at the identity hypothesis, calibrated the same way. Raw: `eval/results/tracklist/*unmixdb_s8_gridA2*`; mixes: `eval/mixes/unmixdb_s8.json`.

Test sets, segments found and entry precision:

| Tempo variant (segments) | No speed grid, smoothing | Speed grid, smoothing | Speed grid, line fits + Viterbi |
|---|---|---|---|
| none (384) | 382 (99.5%), 100% | 382 (99.5%), 99.8% | 382 (99.5%), 99.8% |
| resample, turntable (367) | 210 (57.2%), 93.4% | 325 (88.6%), 96.4% | 322 (87.7%), 99.5% |
| stretch, keylock (384) | 348 (90.6%), 97.8% | 374 (97.4%), 100% | 355 (92.4%), 99.7% |
| All (1,135) | 940 (82.8%), 97.6% | 1,081 (95.2%), 98.8% | 1,059 (93.3%), 99.7% |

- **The speed grid holds on held-out sets with distractors**: turntable recall goes from 57% to 89%, overall from 83% to 95%, at higher precision. Start times: 87.5% within 5 s and 97.3% within 10 s (smoothing).
- **Calibrated on UnmixDB, the line-fit decoder transfers**: 93.3% recall at 99.7% precision, against smoothing's 95.2% at 98.8%. The split-song problem of the set222 run was its synthetic calibration. Calibrated settings: smoothing t_abs 10, t_ratio 3, k 0; line fits min_evidence 50, switch 1.
- **Turntable is the weakest variant.** Of its 367 test segments, 10 are beyond the grid's +/-8% and 16 are double-time, so range alone does not explain the 42 smoothing misses; they are not broken down yet. B2 (one search, any tempo in +/-16%) is the next comparison here.

## B2 triplet fingerprint vs pairs (D31 option B2, D33)

One catalog of 3,000 fma_medium tracks (every val and test positive plus 1,000 other indexed tracks, seeded) indexed both ways: pairs (12.8M postings) and B2 triplets (22.5M postings, 1.76x). Queries: the 1,000 positive and 500 negative test tracks, one 10 s excerpt per condition with the same seeded excerpts and degradations as `eval.run_eval`, each searched once with no speed grid by the in-memory Python matchers. The Go shard returns the same bits: the `internal/match` parity test, and an end-to-end check of 80 speed-changed queries through Go ingest, shard, and gRPC that matched the Python reference in track, score, offset, scale, and postings scanned. Thresholds calibrated per system on val (D13: pooled FAR <= 0.1% over all conditions, accuracy objective over the non-tempo ones); both landed on t_abs 2, t_ratio 2.5. Matcher at commit 003f94c. Raw: `eval/results/b2_eval_seed0.*`.

| Condition (1,000 positives each) | Pairs, accuracy (95% CI) | B2, accuracy (95% CI) | B2 median score |
|---|---|---|---|
| clean | 99.9 (99.4-100.0) | 99.9 (99.4-100.0) | 2,492 |
| white noise, SNR 5 dB | 99.6 (99.0-99.8) | 98.3 (97.3-98.9) | 634 |
| white noise, SNR 0 dB | 98.5 (97.5-99.1) | 95.7 (94.3-96.8) | 265 |
| MP3 64 kbps | 99.9 (99.4-100.0) | 99.9 (99.4-100.0) | 2,385 |
| turntable +1% / -1% | 96.9 / 96.2 | 99.1 / 99.1 | 203 / 204 |
| turntable +2% / -2% | 14.1 / 14.5 | 99.9 / 99.9 | 1,312 / 1,168 |
| turntable +4% / -4% | 0.0 / 0.0 | 99.9 / 99.9 | 1,038 / 707 |
| turntable +8% / -8% | 0.0 / 0.0 | 99.9 / 97.7 (96.6-98.5) | 602 / 145 |
| keylock +4% / -4% | 99.8 / 99.9 | 99.9 / 99.9 | 1,047 / 1,129 |
| keylock +8% / -8% | 99.7 / 99.6 | 99.8 / 99.9 | 502 / 558 |

Test FAR: pairs 0.16%, B2 0.06% (500 negatives x 16 conditions).

- **One B2 search does what took the speed grid's 33.** Every tempo condition is at 97.7-99.9% with a single lookup, where pairs fall to 14% at +/-2% and 0% beyond. The line fit's speed estimate has a median error of 0.02-0.06 percentage points.
- **What it costs.** Heavy noise: 95.7% against 98.5% at SNR 0 dB. Work per search: 2.2x the postings scanned (median over conditions 153,528 against 69,631), but against 33 searches per window for the grid.
- **Turntable scores depend on how far the pitch moves in bands, not only on the tempo change.** Each 1% of turntable speed shifts every peak by about half a log band (36 per octave). Near a whole number of bands (+2%: 1.03 bands, +4%: 2.04, +8%: 4.0) the relative band offsets in the hash survive and scores stay high; near a half band (+/-1%: 0.52, -8%: 4.33) peaks round to neighboring bands inconsistently and the median score is 4-6x lower than at a nearby whole-band shift (+1%: 203 against 1,312 at +2%; -8%: 145 against 602 at +8%). Keylock, which moves no pitch, degrades smoothly with tempo alone. Accuracy holds because even 145 is far above wrong-track scores, but this predicts dips at every half-band shift (about +/-1, 3, 5, 7%) and is the first thing to fix, for example by also fingerprinting the query on a filterbank offset by half a band.

**Rerun with the dual-filterbank query (D35) and the out-of-range fit check** (commit 305c2c1; raw `eval/results/b2_eval_seed0_d35.*`; same catalog, queries, and calibration; thresholds again t_abs 2, t_ratio 2.5): every turntable and keylock condition at 99.8-99.9% (turntable -8%: 97.7% before), clean and MP3 99.9%, white noise SNR 5 dB 98.3% and SNR 0 dB 94.2% (95.7% before; the intervals overlap), test FAR 0.09%. Median postings scanned per query 296,178, 4.3x the pair search's 69,631.

## B2 half-band dips: three variants (D33 follow-up)

Turntable speed from -8% to +8% in 0.5% steps, plus clean, white noise, and keylock, on a 1,300-track catalog (300 test positives plus 1,000 other indexed fma_medium tracks, seeded); 300 positive and 200 negative test tracks, one 10 s excerpt per condition, the same seeded excerpts and degradations as `eval.run_eval`. Every variant uses the thresholds B2 was calibrated to on val in the previous section (t_abs 2, t_ratio 2.5); nothing is tuned here, and it is one seed. Variants: **base** (today's B2); **dual** (the query is also fingerprinted on a filterbank offset by half a band and the two landmark sets are unioned; index unchanged); **interp** (peak band positions refined by a parabola through the neighboring bands, and the hash takes the rounded difference of refined positions; index and query both). Commit e385b9a. Raw: `eval/results/b2_bandshift_seed0.*` (full per-condition table in the `.md`).

| | base | dual | interp |
|---|---|---|---|
| worst turntable step, accuracy | 93.0% (-6.5%) | 99.3% (-7%) | 98.7% (-8%, -6.5%, +7%) |
| lowest per-step median true score | 96 | 438 | 179 |
| clean, accuracy / median score | 99.7% / 2,496 | 99.7% / 2,655 | 99.7% / 2,281 |
| white noise SNR 0 dB | 94.3% | 94.0% | 96.3% |
| FAR over 7,800 negative queries | 0.12% | 0.06% | 0.62% |
| median landmarks / postings scanned per query | 7,125 / 66,160 | 13,914 / 130,335 | 6,691 / 70,480 |

(99.7% is the ceiling here: one positive track returns no candidates in any variant.)

- **The dips are real and periodic**: base scores fall to about 100-250 at every odd percent of turntable speed and recover near whole-band shifts, and the worst steps (-6.5%, +7%) drop to 93-95% accuracy.
- **Dual removes them**: every turntable step is at 99.3-99.7%, the lowest median score rises from 96 to 438, and false accepts halve. The price is about 2x query landmarks and postings scanned, with the index unchanged.
- **Interp helps less and costs specificity**: dips are shallower (worst 98.7%) and heavy noise is better (96.3%), but at the same thresholds it accepts 5x as many negatives (0.62%), so it would need stricter thresholds that give some of that back.

## DJ-mix tracklisting with B2 (one search per window)

The same mixes, windows (10 s every 2.5 s), stop list (4%), decoders, and calibration protocol as the speed-grid sections, searched through a Go shard serving fma_medium plus the UnmixDB references as one B2 triplet index (`cmd/ingest -fingerprint triplets`: 182.5M postings, 1.03 GB, 5.7 bytes per posting). Base B2 is the single-filterbank query (commit 7b67bf3 results, tags `_b2`, `_b2x`); dual B2 is the D35 query (tags `_b2d`, `_b2dx`). On UnmixDB each window also gets x2 and x0.5 hypotheses for double- and half-time; "found" counts known segments, precision counts entries (repeats of a found segment count as correct; the `repeats` field in newer score files counts them).

| Mixes (known segments) | Speed grid, 33-37 searches per window | Base B2, 1 (5 on UnmixDB) | Dual B2, 1 (5 on UnmixDB) |
|---|---|---|---|
| synthetic clean, test (166) | 99.4% found | 165 (99.4%), precision 100% | 165 (99.4%), precision 100% |
| synthetic hard, test (168) | 98.8-100% | 161 (95.8%), 99.4-100% | 165-166 (98.2-98.8%), 97.1-100% |
| UnmixDB, 4 test sets (1,135), smoothing | 1,081 (95.2%), 98.8% | 1,030 (90.7%), 99.9% | 1,017 (89.6%), 99.2% |

Per-window diagnosis on the UnmixDB test sets (windows where one known track plays alone; top candidate right):

- **Distortion is B2's weak spot.** With no tempo change, distorted windows are 88.5% right with dual B2 against 98.9% for the grid; undistorted, 97.1% against 98.8%. Measured on aligned calibration-set segments, distortion leaves 38% of B2 peaks (27% of pair peaks) within a frame and a band, but only 1.9% of B2 hashes survive at the best offset against 5.4% of pair hashes: a triplet needs three surviving peaks, a pair two. Bass and compressor effects cost B2 no more than pairs (28% and 51% of hashes survive, pairs 28% and 50%).
- **Double- and half-time.** Undoing x2 on the audio (resample for turntable) finds 79% of turntable double-time windows (the grid 0%); WSOLA keylock at x2 finds 12% (the grid, which rescales pair landmarks, 97%). Rescaling B2 landmarks instead (commit e2fda35, tag `_b2dx`, reverted) fails: B2 picks 30 peaks per second of mix, so at double tempo it keeps half the song's peaks and forms different triplets; peak picking has to see the song at its own tempo.
- **Keylock (sox stretch) within +/-8.5%:** 92-96% of windows for B2, 98-100% for the grid. **Turntable beyond 8.5%:** B2 80% (dual), grid 26%.
- **B2 and the grid's unchanged-window pair search are complementary**: a window is right under base B2 or under one pair search 92.1% of the time (B2 alone 86.0%, the pair search alone 82.0%, the full grid 93.4%); turntable 86.3% against the grid's 84.8%, keylock 90.8% against 96.5%. This is an upper bound for a hybrid that would need a rule to pick between the two answers.

## DJ-mix tracklisting with the hybrid matcher (D36)

Per window: dual B2 on the B2 index, as is and with x2 and x0.5 turntable undone on the audio (tag `_b2dh`), plus the pair searches of the grid run on the pair index: the unchanged window and x2 and x0.5 keylock undone on its landmarks (from `_gridA2`); 6 searches per window. Pair scores and runner-ups are multiplied by a weight calibrated with the decoder settings on set222 + set275 (chosen: 3.0 for both decoders); test on the other four sets, as before. Raw: `eval/results/tracklist/*b2dh*`.

UnmixDB test sets (1,135 known segments), songs found and precision:

| | Speed grid (37 searches) | B2 only (3) | Hybrid, plain windows only (2) | Hybrid (6) |
|---|---|---|---|---|
| Smoothing | 1,081 (95.2%), 98.8% | 1,018 (89.7%), 99.8% | 1,068 (94.1%), 98.3% | **1,088 (95.9%), 97.9%** |
| Line fits + Viterbi | 1,059 (93.3%), 99.7% | 1,051 (92.6%), 95.5% | 1,055 (93.0%), 97.2% | 1,047 (92.2%), 98.4% |

Hybrid with smoothing, by tempo variant: unchanged 382/384, turntable 336/367 (grid 325), keylock 370/384 (grid 374). Synthetic test mixes (B2 rows from `_b2d`, pair rows from `_gridA`): clean 165/166 at 100% precision, hard 166/168 at 97.1% (smoothing) and 100% (line fits).

- **The hybrid matches the grid at a sixth of the searches**: 95.9% of songs against 95.2%, precision 97.9% against 98.8% (smoothing); turntable is better than the grid, keylock slightly worse. Two searches per window (plain windows only) already reach 94.1%. A B2 search cost about 5.9 pair searches of shard capacity (BENCHMARKS, B7), so in cost the hybrid was about 1.8x cheaper than the grid per window; with pruned B2 searches (D37, B8) it is 3.5-4.2x cheaper, and the two-search version 10-13x.
- **Merged as `cmd/tracklist` prints it** (`--merge-gap 2.5`): the same songs found, entries 1,258 to 1,122 and repeat entries 143 to 10, precision 97.9%.
- **Start times**: 94.3% within 10 s (smoothing).
- The line-fit decoder gains less from the hybrid here (92.2%); smoothing is the better decoder for it on this data.
- **The shipped command reproduces it.** `cmd/tracklist` (Go fingerprints, both clusters through their coordinators, calibrated defaults) run over set044's 96 mixes gives the same entries as the eval's decode of those mixes, to within 10 ms in start and end and 0.01 points in tempo, on all 96: 273 of 285 songs found (95.8%), precision 97.2%, in both. Check: `eval/score_cli.py`; the command's output: `eval/results/tracklist/cli_runs_unmixdb_s8_set044.jsonl`.

## Pruned B2 searches (D37)

B2 searches now let only the 200 tracks with the most matches vote (D37). Everything above was rerun with it, with fresh calibration (commit 7d69058).

- **Single 10 s excerpts, 3,000 tracks** (`eval/results/b2_eval_seed0_d37.*`): every turntable and keylock condition still 99.8-99.9%, clean and MP3 99.9%, SNR 5 dB 98.4%, SNR 0 dB 94.9% (94.2% unpruned), test FAR 0.10%. The calibrated t_abs rose from 2 to 44: the runner-up now comes from the pruned set, so the absolute score test carries more of the work.
- **Tracklisting** (tags `_b2dp`, `_b2dhp`): synthetic test mixes unchanged (clean 165/166 at 100%, hard 166/168 at 99.4% with smoothing). UnmixDB test sets, smoothing: hybrid 1,080/1,135 (95.2%) at 98.9% precision (unpruned 95.9% at 97.9%; F1 0.970 against 0.969), by variant unchanged 383/384, turntable 328/367, keylock 369/384; two-search hybrid 93.7%; B2 alone 90.7% (unpruned 89.7%). Calibrated hybrid settings: t_abs 10, t_ratio 2, k 1, pair weight 3; `cmd/tracklist` defaults to them.

## Live stream monitoring (D38, D39, D40)

Streams are decoded as audio arrives, cut into 10 s windows every 2.5 s, searched with the hybrid (D36), and decoded online: fixed-lag smoothing with k = 1 (D38), so a window's label is final one window later. Events go to the stream's log at the worker's lease epoch (D39, D40). Design: `docs/design/live-monitoring.md`.

- **Live output equals offline output.** All 96 mixes of set044 played as file streams through the registry and one worker (8 streams at a time) log the same entries as `cmd/tracklist` gives offline, value for value (track, start, end, tempo, evidence): 274 entries each, 0 of 96 mixes differ, no window left unanswered. Both equal the eval's decode of the same mixes: 268 of 285 songs found (94.0%), precision 97.8%. Run: `eval/results/live/equiv_set044/` (`python -m bench.scripts.live equiv`), commit 43bae83; a correctness check run on battery, so no timings from it are reported. **All four test sets** (384 mixes, `eval/results/live/equiv_test/`, binaries built at 8dca424, AC power): 0 of 384 mixes differ, 1,095 entries each, no window unanswered, and `cmd/tracklist`'s entries equal the eval's decode of every mix (to 10 ms): 1,080 of 1,135 songs found (95.2%), precision 98.9%, the hybrid's test result above (`python -m eval.score_cli`). Detection delay over those 1,080 songs: median 12.5 s, p10 7.8 s, p90 16.1 s. The decoder's equality with offline decoding is also unit-tested on random window sequences (k 0 to 3, five merge gaps) and the golden rows (`internal/tracklist/online_test.go`).
- **A hop that is a multiple of 128 samples** (throughput option T1 (a) needs one). At 27,520 samples (2.4961 s; `eval/results/live/equiv_test_hop27520/`, same binaries), live still equals offline on all 384 test mixes (1,104 entries each). Scored on the test sets, with the decoder as calibrated at 2.5 s unless noted (`eval/results/tracklist/*cli_h27520*`, `*cli_h27648*`: rows from `cmd/tracklist -rows`, which at 2.5 s equal the eval's rows value for value; `bench/scripts/tracklist_rows.py`):

  | Hop | Songs found | Wrong entries | Precision | F1 |
  |---|---|---|---|---|
  | 2.5 s (today) | 1,080/1,135 (95.2%) | 12 | 98.9% | 0.970 |
  | 27,520 samples (2.4961 s) | 1,082 (95.3%) | 20 | 98.2% | 0.967 |
  | 27,648 samples (2.5078 s), a control | 1,080 (95.2%) | 18 | 98.4% | 0.967 |
  | 27,520, recalibrated on set222 and set275 (t_abs 40, t_ratio 2, k 0) | 1,085 (95.6%) | 31 | 97.2% | 0.964 |
  | 2.5 s, every window started on the next multiple of 128 samples (`-snap 128`) | 1,081 (95.2%) | 18 | 98.4% | 0.968 |
  | 2.5 s, every window started 32 samples late (`-offset 32`), a placement control | 1,082 (95.3%) | 14 | 98.7% | 0.970 |
  | 2.5 s, 64 samples late (`-offset 64`) | 1,080 (95.2%) | 14 | 98.7% | 0.969 |

  Every variant finds the same songs. What separates them is whether the windows share one frame grid, which the shared front end needs: the three placements of today's grid log 12 to 14 wrong entries, the three that put every window on a multiple of 128 samples (two hops and snapping, which moves each window at most 11.5 ms) log 18 to 20. Paired by mix, the aligned variants are worse in 7 or 8 mixes and better in 0 to 3; moving today's windows by 32 or 64 samples changes 4 mixes either way. Likely cause: today's hop is 42 samples past a multiple of 64, so consecutive windows see a song on different sub-grid frame phases and their mistakes are less correlated, which the majority vote over neighboring windows exploits; on one shared grid a song's windows all share a phase. Recalibrating does not win it back: the calibration sets (425 songs) cannot tell settings this close apart, and the one they pick does worse on the test sets. So the shared front end costs about half a point of precision (F1 0.970 to 0.967), and keeping the hop (snapping) does not avoid it. (The UnmixDB transitions are not on a 2.5 s grid.)
- **Both clusters need the 4% stop list** the hybrid was calibrated with (D30, D37). A first run without it found 264 of 285 songs through `cmd/tracklist`, with live and offline still identical: more matches per search raise the scores, and the calibrated thresholds no longer fit. The harness now sets it, and `cmd/tracklist` says so.
- **Detection delay, stream time.** A start event is decided when its entry's first window and one more are in: 12.5 s after that window starts. From each found song's true transition (the middle of the crossfade into it) to that point: median 12.5 s, p10 8.2 s, p90 14.9 s, max 31.0 s over the 268 songs (`delay.json`, `python -m eval.live_score --equiv`). In real time the searches' lag comes on top.
- **Real time** (BENCHMARKS B9): with 24 stations playing test mixes live to one worker, 95.3% of songs found at 98.6% precision, each reported a median 12.6 s after its transition; one laptop keeps up with 32 stations (95.5% of songs found) but not 48, and with 48 (94.9%) after D44 made the query fingerprint 1.55x cheaper with identical output. With a worker killed mid-broadcast, its 16 streams moved to a standby worker, each losing 5.2 s of audio, with no sequence gaps; 15 carried their song in progress across the handoff as one entry, and none was reported twice because of it.

## Against open-source systems (D41)

Landmark next to audfprint (Ellis; landmark pairs, the family of Landmark's pair fingerprint) and Panako 2.1 (Six; tempo- and pitch-robust triplets, the family of B2). Design: `docs/design/head-to-head.md`; harness `eval/h2h.py`; results `eval/results/h2h/`.

- **Same data for every system.** Each indexes the same 24,287 tracks (fma_medium's indexed tracks plus the 302 UnmixDB references) and answers the same files: 10 s excerpts of 1,000 positives and 500 never-indexed negatives per split, degraded per condition, written at 22,050 Hz so each tool decodes at its own rate (Landmark and audfprint 11,025 Hz, Panako 16,000 Hz).
- **Same accept rule.** Each system's thresholds (minimum top score, and top over the best score outside the top's duplicate cluster) are calibrated on val to a pooled false-accept rate of at most 0.1%, maximizing mean accuracy over all ten conditions; test is reported.
- **One tuned setting per tool, chosen on val** (D41 H3): audfprint `--density` 80 (tried 10, 20 default, 40, 60, 80, 100; mean val accuracy 0.43 at the default, 0.61 at 80, 0.60 at 100), Panako's event-point time filter 15 (tried 25 default and 15; 0.74 and 0.80). Landmark: the pair and the triplet fingerprint, searched separately (stop list 4%).

Test accuracy (positives identified and accepted; false accepts at most 0.6% in any cell, 0.1% pooled on val by construction):

| Condition | Landmark pairs | Landmark triplets | audfprint 20 (default) | audfprint 80 | Panako 25 (default) | Panako 15 |
|---|---|---|---|---|---|---|
| Clean | 99.9% | 99.9% | 98.8% | 99.8% | 94.2% | 94.2% |
| White noise 10 dB | 99.6% | 99.6% | 72.4% | 89.6% | 73.7% | 79.0% |
| White noise 0 dB | 98.1% | 96.8% | 32.2% | 54.0% | 38.0% | 52.0% |
| MP3 64 kbps | 99.9% | 99.9% | 98.3% | 99.8% | 91.1% | 93.1% |
| Turntable +2% | 0.2% | 99.9% | 0.6% | 6.2% | 79.0% | 82.0% |
| Turntable -5% | 0.1% | 99.9% | 0.0% | 0.1% | 78.2% | 72.5% |
| Turntable +8% | 0.0% | 99.9% | 0.0% | 0.0% | 59.2% | 73.4% |
| Keylock -2% | 99.9% | 99.9% | 92.0% | 99.8% | 88.1% | 89.6% |
| Keylock +5% | 99.8% | 99.9% | 26.9% | 93.1% | 76.1% | 84.8% |
| Keylock -8% | 97.8% | 99.7% | 2.6% | 56.5% | 84.4% | 77.0% |

- **Noise is where the pair systems part ways**: at 0 dB, Landmark's pair fingerprint keeps 98.1% against audfprint's 54.0% even after audfprint's tuning; both find almost every clean or MP3 clip.
- **Tempo is where the triplet systems part ways**: Landmark's triplet fingerprint identifies 99.7-99.9% of clips at every turntable and keylock step tested; Panako, built for the same problem, 72.5-89.6% at its tuned setting, and 52-94% across the other conditions.

DJ mixes (UnmixDB test sets, 1,135 known songs): every system's per-window answers through the same smoothing decoder, its settings calibrated on set222 and set275 with the merge step `cmd/tracklist` applies, scored once on the test sets (`eval/results/tracklist/score_unmixdb_s8_*_merged.json`).

| System | Searches per window | Songs found | Precision | Unchanged | Turntable | Keylock |
|---|---|---|---|---|---|---|
| Landmark hybrid (D36, D37) | 6 | 1,080 (95.2%) | 98.9% | 383/384 | 328/367 | 369/384 |
| audfprint, density 80 | 1 | 909 (80.1%) | 97.0% | 379/384 | 209/367 | 321/384 |
| Panako, time filter 15 | 1 | 892 (78.6%) | 95.4% | 335/384 | 260/367 | 297/384 |

- Landmark's hybrid spends 6 searches per window; its triplet search alone (3 searches, with double and half time) found 90.7% (pruned B2, above).
- Cost (BENCHMARKS B11): per clip on one core, Landmark's pair search 75 ms and audfprint 84 ms; Landmark's triplet search 179 ms and Panako 235 ms; Landmark's triplet index takes a quarter of Panako's space per track.
- Limitations: one machine; each tool as shipped with one tuned setting (more tuning could help it: audfprint got six densities, Panako two time filters, and Panako was still improving at the denser one); Panako's native transform crashed several times while indexing (resumed, every track stored); open-source systems only, not commercial services.

## DJ mix analysis: gain curves (D43, Python prototype)

How each song was faded in and out, not only which songs played. For each tracklisted song, its reference is warped onto the mix by its triplet alignment (reference time = a + rate x mix time; and a pitch shift of 36 log2(rate) bands when the fit says turntable rather than keylock), and per spectrogram frame the mix's band powers are split into non-negative gains of the songs playing there. Each fade is read off the gain curve by fitting a linear ramp. Checked against UnmixDB's labels (linear crossfades, per its README). `eval/dj_analysis.py`; results `eval/results/dj_analysis/`.

- **Method chosen on the calibration sets** (set222, set275, 393 songs): the ramp fit over 10%/90% crossings, for steadier tails; a band-edge bug (a pitch shift zeroed the 55 Hz band, so turntable never won) was found and fixed there. Development started on set044, a test set, before moving to the calibration sets; the choices were made on calibration data.
- **Test sets** (set044, set123, set230, set281; 1,058 songs; scored once). Median error of each fade edge, against the window decoder alone (a song's fade-in spans its entry's first 10 s window, its fade-out the last):

| Fade edge | Gain curves | Window decoder |
|---|---|---|
| Fade-in start | 0.77 s | 2.62 s |
| Fade-in end | 1.80 s | 4.80 s |
| Fade-out start | 1.53 s | 2.88 s |
| Fade-out end | 1.63 s | 2.18 s |

- The tails are mixed: the 90th percentile is better than the window decoder for fade-ins (7.7 and 6.7 s against 10.1 and 12.5 s) and worse for fade-outs (8.1 and 9.1 s against 6.8 and 5.7 s).
- Gain-curve error against the labeled ramp, inside fades: median 0.28 (gain 0 to 1). By effect: 0.08 with no effect, 0.16 with the bass swap, 0.41 and 0.39 with the compressor and distortion, which change the sound non-linearly, so the additive power model fits worse.
- Turntable vs keylock: right for 94.7% of the 225 songs changed by more than 2% (no effect 100%, bass swap 95.5%, compressor 93.8%, distortion 86.4%), and 88.5% of the 338 changed by more than 0.5%, where the pitch shift is under a third of a band.
- **Tempo of the set (D43 A2).** Each reference's BPM is estimated once (librosa's global tempo estimate: onset strength, then an autocorrelation tempogram under a log-normal prior; its beat tracker segfaults in this environment), and a song's BPM in the set is that times its triplet-fitted playback rate. Checked against UnmixDB's own BPM labels (from IRCAM's beat tracker) times each mix's speed factors, within 4%. On the calibration sets the prior was chosen: librosa's default (120 BPM, one octave wide) got 19 of the 25 references right, a club-music prior (125 BPM, half an octave) 24. On the test sets (scored once): 82 of 86 references within 4% (95.3%; 97.7% allowing double or half tempo), and the set's BPM right for 95.1% of 1,058 songs (97.6%). The fitted playback rate is off by a median of 0.013 percentage points, so nearly all the error is the reference estimate (`bpm_summary.json`, `bpm_refs.json`).
- **Every labeled fade.** Scoring the same test results against UnmixDB's own label files, which also label the first song's fade-in from silence and the last song's fade-out (`python -m eval.dj_analysis --rescore`, `test/label_summary.json`; method unchanged): 1,058 fades of each kind, median errors 0.30 s (fade-in start), 1.83 s (fade-in end), 1.61 s (fade-out start), and 1.33 s (fade-out end), against the window decoder's 1.17, 3.23, 3.28, and 1.81 s.
- **Go port** (`internal/analysis`, `cmd/tracklist -analyze`, D43 A3): on all 384 test mixes it gives the prototype's answer for every one of the 1,058 songs: the same turntable or keylock call and the same fades to within 0.1 s (largest difference 0.00 s; `bench/analysisparity`, `test/go_parity.json`). The per-frame fit tries every subset of the songs playing (at most a few), which is exact non-negative least squares at that size.
- Limitations: needs the reference audio; effects beyond gain are outside the model; fades are read as linear ramps (UnmixDB's are). The converted labels give a mix's first song no fade-in and its last no fade-out (they are measured from the mix's start and end for tracklisting), so the table above leaves those fades out.

## Go system parity (Phase 2 done criterion)

The test split was rerun end to end through the Go stack: `cmd/ingest` segment (P=1, varint), `cmd/shard` over gRPC, Go matcher, with Python fingerprints (Go fingerprints are identical by D17 parity). Of 21,978 query rows, **0 differ** from the Python run (`queries_test_seed0_f64.csv.gz` vs `queries_test_seed0_goshard.csv.gz`) in top-1 track, score, runner-up, offset, postings scanned, or correctness, so every accuracy and FAR figure above holds for the Go system unchanged.

## Phase 1 baseline (fma_small, seed 0, no tag)


- Index: 6,997 tracks (fma_small minus held-out negatives and 2 undecodable files). Stats: `eval/results/index_stats_splits_small_seed0.json`.
- Queries: 1,000 positive tracks (one per duplicate cluster) and 500 negative tracks x 2 excerpts per condition, test split. Raw rows: `eval/results/queries_test_seed0.csv.gz`.
- Thresholds calibrated on val only: see `eval/results/summary_seed0.json`.
- "Top-1 raw" ignores thresholds. "Accuracy" counts a rejected correct match as a miss. FAR = accepted negatives. All intervals are Wilson 95%.
- 22 query rows per split were skipped: one track per split is shorter than the excerpt (logged in the CSV `error` column).

| Condition | Top-1 raw (95% CI) | Accuracy at threshold (95% CI) | FAR (95% CI) | n pos / neg |
|---|---|---|---|---|
| clean_10s | 100.0 (99.6-100.0) | 99.2 (98.4-99.6) | 0.1 (0.0-0.6) | 1000 / 998 |
| clean_5s | 100.0 (99.6-100.0) | 99.1 (98.3-99.5) | 0.2 (0.1-0.7) | 1000 / 998 |
| mp3_128k_10s | 100.0 (99.6-100.0) | 99.2 (98.4-99.6) | 0.1 (0.0-0.6) | 1000 / 998 |
| mp3_64k_10s | 100.0 (99.6-100.0) | 99.2 (98.4-99.6) | 0.2 (0.1-0.7) | 1000 / 998 |
| speed_+2pct_10s | 39.2 (36.2-42.3) | 1.5 (0.9-2.5) | 0.0 (0.0-0.4) | 1000 / 998 |
| speed_-2pct_10s | 43.1 (40.1-46.2) | 2.2 (1.5-3.3) | 0.0 (0.0-0.4) | 1000 / 998 |
| white_snr0_10s | 99.0 (98.2-99.5) | 93.5 (91.8-94.9) | 0.0 (0.0-0.4) | 1000 / 998 |
| white_snr10_10s | 99.9 (99.4-100.0) | 98.5 (97.5-99.1) | 0.1 (0.0-0.6) | 1000 / 998 |
| white_snr15_10s | 100.0 (99.6-100.0) | 98.7 (97.8-99.2) | 0.1 (0.0-0.6) | 1000 / 998 |
| white_snr5_10s | 99.8 (99.3-99.9) | 96.9 (95.6-97.8) | 0.1 (0.0-0.6) | 1000 / 998 |
| white_snr5_5s | 99.0 (98.2-99.5) | 93.2 (91.5-94.6) | 0.0 (0.0-0.4) | 1000 / 998 |

### Known issues
- **Speed +/-2% fails** (expected for absolute (f, dt) hashes, brief section 13). Raw top-1 is ~40% only because a few landmarks still align.
- **Duplicates vs the ratio test:** fixed by D14 (clean 10 s 99.2 to 100.0).
- **Sub-hop misalignment:** addressed by D15 at the query side. Block-boundary ranking (D9) is still not shift-invariant; not yet addressed.
