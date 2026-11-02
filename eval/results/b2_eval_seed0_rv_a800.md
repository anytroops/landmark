# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on test (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 99.9 (99.4-100.0) |
| speed_+1pct_10s | 99.9 (99.4-100.0) |
| speed_+8pct_10s | 99.2 (98.4-99.6) |
| speed_-8pct_10s | 98.2 (97.2-98.9) |
| keylock_+8pct_10s | 99.2 (98.4-99.6) |
| keylock_-8pct_10s | 99.2 (98.4-99.6) |
| demand_pcafeter_snr0_10s | 83.1 (80.7-85.3) |
| demand_pstation_snr0_10s | 83.9 (81.5-86.0) |
| room_rt400_drr5_clean_10s | 99.3 (98.6-99.7) |
| room_rt800_drr-5_clean_10s | 76.3 (73.6-78.8) |
| room_rt1500_drr-10_clean_10s | 31.0 (28.2-33.9) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 33.9 (31.0-36.9) |
| room_rt800_drr-5_keylock_+8pct_10s | 55.5 (52.4-58.6) |
| room_rt800_drr-5_speed_+8pct_10s | 42.2 (39.2-45.3) |

b2: t_abs 39, t_ratio 2.5, test FAR 0.10%, median postings scanned per query 224,142
