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
