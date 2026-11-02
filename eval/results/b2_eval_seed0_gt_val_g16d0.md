# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on val (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 100.0 (99.2-100.0) |
| speed_+1pct_10s | 99.8 (98.9-100.0) |
| speed_+8pct_10s | 98.6 (97.1-99.3) |
| speed_-8pct_10s | 98.6 (97.1-99.3) |
| keylock_+8pct_10s | 99.6 (98.6-99.9) |
| keylock_-8pct_10s | 99.0 (97.7-99.6) |
| demand_pcafeter_snr0_10s | 83.6 (80.1-86.6) |
| demand_pstation_snr0_10s | 87.4 (84.2-90.0) |
| room_rt400_drr5_clean_10s | 99.4 (98.3-99.8) |
| room_rt800_drr-5_clean_10s | 72.0 (67.9-75.8) |
| room_rt1500_drr-10_clean_10s | 29.6 (25.8-33.7) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 28.6 (24.8-32.7) |
| room_rt800_drr-5_keylock_+8pct_10s | 56.8 (52.4-61.1) |
| room_rt800_drr-5_speed_+8pct_10s | 45.8 (41.5-50.2) |

b2: t_abs 30, t_ratio 3.0, val FAR 0.10%, median postings scanned per query 160,698
