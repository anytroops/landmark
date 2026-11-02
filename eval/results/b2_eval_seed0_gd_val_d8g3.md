# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on val (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 99.8 (98.9-100.0) |
| speed_+1pct_10s | 99.8 (98.9-100.0) |
| speed_+8pct_10s | 99.4 (98.3-99.8) |
| speed_-8pct_10s | 99.4 (98.3-99.8) |
| keylock_+8pct_10s | 99.2 (98.0-99.7) |
| keylock_-8pct_10s | 98.6 (97.1-99.3) |
| demand_pcafeter_snr0_10s | 79.0 (75.2-82.3) |
| demand_pstation_snr0_10s | 86.8 (83.6-89.5) |
| room_rt400_drr5_clean_10s | 98.4 (96.9-99.2) |
| room_rt800_drr-5_clean_10s | 70.6 (66.5-74.4) |
| room_rt1500_drr-10_clean_10s | 30.4 (26.5-34.6) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 35.8 (31.7-40.1) |
| room_rt800_drr-5_keylock_+8pct_10s | 49.0 (44.6-53.4) |
| room_rt800_drr-5_speed_+8pct_10s | 46.0 (41.7-50.4) |

b2: t_abs 33, t_ratio 3.0, val FAR 0.10%, median postings scanned per query 48,564
