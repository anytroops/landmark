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
- **These mixes are easy**: clean audio, short 30 s source clips, and no EQ, filtering, reverb, or crowd noise. Both decoders are near-perfect on them, so they do not yet separate the methods; harder mixes and real ones (Mixotic) are next. The cost is 33 searches per window instead of 1.

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
