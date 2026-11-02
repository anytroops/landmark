# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on val (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 99.8 (98.9-100.0) |
| speed_+1pct_10s | 99.8 (98.9-100.0) |
| speed_+8pct_10s | 99.6 (98.6-99.9) |
| speed_-8pct_10s | 99.4 (98.3-99.8) |
| keylock_+8pct_10s | 99.8 (98.9-100.0) |
| keylock_-8pct_10s | 99.6 (98.6-99.9) |
| demand_pcafeter_snr0_10s | 87.8 (84.6-90.4) |
| demand_pstation_snr0_10s | 92.0 (89.3-94.1) |
| room_rt400_drr5_clean_10s | 99.6 (98.6-99.9) |
| room_rt800_drr-5_clean_10s | 78.2 (74.4-81.6) |
| room_rt1500_drr-10_clean_10s | 25.8 (22.2-29.8) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 37.4 (33.3-41.7) |
| room_rt800_drr-5_keylock_+8pct_10s | 52.4 (48.0-56.7) |
| room_rt800_drr-5_speed_+8pct_10s | 45.6 (41.3-50.0) |

b2: t_abs 45, t_ratio 2.0, val FAR 0.10%, median postings scanned per query 105,922
