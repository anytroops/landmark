# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on val (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 100.0 (99.2-100.0) |
| speed_+1pct_10s | 99.8 (98.9-100.0) |
| speed_+8pct_10s | 99.6 (98.6-99.9) |
| speed_-8pct_10s | 99.2 (98.0-99.7) |
| keylock_+8pct_10s | 99.2 (98.0-99.7) |
| keylock_-8pct_10s | 99.4 (98.3-99.8) |
| demand_pcafeter_snr0_10s | 80.8 (77.1-84.0) |
| demand_pstation_snr0_10s | 86.6 (83.3-89.3) |
| room_rt400_drr5_clean_10s | 99.0 (97.7-99.6) |
| room_rt800_drr-5_clean_10s | 71.2 (67.1-75.0) |
| room_rt1500_drr-10_clean_10s | 29.6 (25.8-33.7) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 38.2 (34.0-42.5) |
| room_rt800_drr-5_keylock_+8pct_10s | 49.4 (45.0-53.8) |
| room_rt800_drr-5_speed_+8pct_10s | 49.6 (45.2-54.0) |

b2: t_abs 32, t_ratio 3.0, val FAR 0.10%, median postings scanned per query 42,801
