# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on val (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 98.8 (97.4-99.4) |
| speed_+1pct_10s | 98.2 (96.6-99.1) |
| speed_+8pct_10s | 96.0 (93.9-97.4) |
| speed_-8pct_10s | 93.4 (90.9-95.3) |
| keylock_+8pct_10s | 94.4 (92.0-96.1) |
| keylock_-8pct_10s | 94.4 (92.0-96.1) |
| demand_pcafeter_snr0_10s | 66.4 (62.1-70.4) |
| demand_pstation_snr0_10s | 72.2 (68.1-75.9) |
| room_rt400_drr5_clean_10s | 92.6 (90.0-94.6) |
| room_rt800_drr-5_clean_10s | 50.6 (46.2-55.0) |
| room_rt1500_drr-10_clean_10s | 15.8 (12.9-19.3) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 23.8 (20.3-27.7) |
| room_rt800_drr-5_keylock_+8pct_10s | 34.4 (30.4-38.7) |
| room_rt800_drr-5_speed_+8pct_10s | 28.0 (24.2-32.1) |

b2: t_abs 35, t_ratio 2.5, val FAR 0.10%, median postings scanned per query 13,158
