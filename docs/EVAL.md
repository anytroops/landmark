# Evaluation

Methodology: `docs/PROJECT_BRIEF.md` section 13, splits per D12, thresholds per D13. Every number here traces to a file in `eval/results/`.

## Phase 1: Python prototype on fma_small (seed 0)

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
- **Duplicates vs the ratio test:** every clean positive that was correct but rejected has a duplicate in the index scoring nearly as high (one exact 778/778 tie). Pending decision.
- **Sub-hop misalignment:** a clean excerpt at a random sample offset matches far fewer of its landmarks than a frame- and block-aligned one: [TBD], needs a committed measurement. Pending decision.
