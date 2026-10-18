# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on test (95% Wilson CI); thresholds calibrated on val.

| Condition | Pairs | B2 |
|---|---|---|
| clean_10s | 99.9 (99.4-100.0) | 99.9 (99.4-100.0) |
| white_snr5_10s | 99.6 (99.0-99.8) | 98.3 (97.3-98.9) |
| white_snr0_10s | 98.5 (97.5-99.1) | 94.2 (92.6-95.5) |
| mp3_64k_10s | 99.9 (99.4-100.0) | 99.9 (99.4-100.0) |
| speed_+1pct_10s | 96.9 (95.6-97.8) | 99.9 (99.4-100.0) |
| speed_-1pct_10s | 96.2 (94.8-97.2) | 99.9 (99.4-100.0) |
| speed_+2pct_10s | 14.1 (12.1-16.4) | 99.9 (99.4-100.0) |
| speed_-2pct_10s | 14.5 (12.5-16.8) | 99.9 (99.4-100.0) |
| speed_+4pct_10s | 0.0 (0.0-0.4) | 99.9 (99.4-100.0) |
| speed_-4pct_10s | 0.0 (0.0-0.4) | 99.9 (99.4-100.0) |
| speed_+8pct_10s | 0.0 (0.0-0.4) | 99.9 (99.4-100.0) |
| speed_-8pct_10s | 0.0 (0.0-0.4) | 99.9 (99.4-100.0) |
| keylock_+4pct_10s | 99.8 (99.3-99.9) | 99.9 (99.4-100.0) |
| keylock_-4pct_10s | 99.9 (99.4-100.0) | 99.9 (99.4-100.0) |
| keylock_+8pct_10s | 99.7 (99.1-99.9) | 99.9 (99.4-100.0) |
| keylock_-8pct_10s | 99.6 (99.0-99.8) | 99.8 (99.3-99.9) |

pairs: t_abs 2, t_ratio 2.5, test FAR 0.16%, median postings scanned per query 69,631

b2: t_abs 2, t_ratio 2.5, test FAR 0.09%, median postings scanned per query 296,178
