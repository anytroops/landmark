# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on val (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 100.0 (99.2-100.0) |
| speed_+1pct_10s | 99.8 (98.9-100.0) |
| speed_+8pct_10s | 98.8 (97.4-99.4) |
| speed_-8pct_10s | 98.6 (97.1-99.3) |
| keylock_+8pct_10s | 99.6 (98.6-99.9) |
| keylock_-8pct_10s | 99.4 (98.3-99.8) |
| demand_pcafeter_snr0_10s | 86.0 (82.7-88.8) |
| demand_pstation_snr0_10s | 88.6 (85.5-91.1) |
| room_rt400_drr5_clean_10s | 99.4 (98.3-99.8) |
| room_rt800_drr-5_clean_10s | 76.8 (72.9-80.3) |
| room_rt1500_drr-10_clean_10s | 30.2 (26.3-34.4) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 34.4 (30.4-38.7) |
| room_rt800_drr-5_keylock_+8pct_10s | 57.8 (53.4-62.1) |
| room_rt800_drr-5_speed_+8pct_10s | 41.8 (37.6-46.2) |

b2: t_abs 40, t_ratio 2.5, val FAR 0.10%, median postings scanned per query 219,165
