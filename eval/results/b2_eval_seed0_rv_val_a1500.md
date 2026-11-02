# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on val (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 99.8 (98.9-100.0) |
| speed_+1pct_10s | 99.8 (98.9-100.0) |
| speed_+8pct_10s | 97.2 (95.4-98.3) |
| speed_-8pct_10s | 93.6 (91.1-95.4) |
| keylock_+8pct_10s | 98.4 (96.9-99.2) |
| keylock_-8pct_10s | 98.4 (96.9-99.2) |
| demand_pcafeter_snr0_10s | 77.0 (73.1-80.5) |
| demand_pstation_snr0_10s | 83.4 (79.9-86.4) |
| room_rt400_drr5_clean_10s | 98.8 (97.4-99.4) |
| room_rt800_drr-5_clean_10s | 75.2 (71.2-78.8) |
| room_rt1500_drr-10_clean_10s | 35.0 (30.9-39.3) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 27.0 (23.3-31.1) |
| room_rt800_drr-5_keylock_+8pct_10s | 57.4 (53.0-61.7) |
| room_rt800_drr-5_speed_+8pct_10s | 39.6 (35.4-44.0) |

b2: t_abs 2, t_ratio 3.0, val FAR 0.04%, median postings scanned per query 223,073
