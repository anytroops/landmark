# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on val (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 100.0 (99.2-100.0) |
| speed_+1pct_10s | 99.8 (98.9-100.0) |
| speed_+8pct_10s | 99.4 (98.3-99.8) |
| speed_-8pct_10s | 99.0 (97.7-99.6) |
| keylock_+8pct_10s | 99.8 (98.9-100.0) |
| keylock_-8pct_10s | 99.6 (98.6-99.9) |
| demand_pcafeter_snr0_10s | 88.0 (84.9-90.6) |
| demand_pstation_snr0_10s | 91.6 (88.8-93.7) |
| room_rt400_drr5_clean_10s | 99.4 (98.3-99.8) |
| room_rt800_drr-5_clean_10s | 80.6 (76.9-83.8) |
| room_rt1500_drr-10_clean_10s | 28.2 (24.4-32.3) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 38.0 (33.9-42.3) |
| room_rt800_drr-5_keylock_+8pct_10s | 57.0 (52.6-61.3) |
| room_rt800_drr-5_speed_+8pct_10s | 42.0 (37.8-46.4) |

b2: t_abs 45, t_ratio 2.0, val FAR 0.09%, median postings scanned per query 219,570
