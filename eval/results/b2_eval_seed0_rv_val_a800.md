# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on val (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 100.0 (99.2-100.0) |
| speed_+1pct_10s | 99.8 (98.9-100.0) |
| speed_+8pct_10s | 98.8 (97.4-99.4) |
| speed_-8pct_10s | 98.0 (96.4-98.9) |
| keylock_+8pct_10s | 99.2 (98.0-99.7) |
| keylock_-8pct_10s | 99.2 (98.0-99.7) |
| demand_pcafeter_snr0_10s | 85.4 (82.0-88.2) |
| demand_pstation_snr0_10s | 88.4 (85.3-90.9) |
| room_rt400_drr5_clean_10s | 99.6 (98.6-99.9) |
| room_rt800_drr-5_clean_10s | 78.2 (74.4-81.6) |
| room_rt1500_drr-10_clean_10s | 32.6 (28.6-36.8) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 32.6 (28.6-36.8) |
| room_rt800_drr-5_keylock_+8pct_10s | 59.0 (54.6-63.2) |
| room_rt800_drr-5_speed_+8pct_10s | 47.6 (43.3-52.0) |

b2: t_abs 39, t_ratio 2.5, val FAR 0.10%, median postings scanned per query 223,597
