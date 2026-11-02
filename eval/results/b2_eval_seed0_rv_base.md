# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on test (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 99.9 (99.4-100.0) |
| speed_+1pct_10s | 99.9 (99.4-100.0) |
| speed_+8pct_10s | 99.4 (98.7-99.7) |
| speed_-8pct_10s | 98.8 (97.9-99.3) |
| keylock_+8pct_10s | 99.4 (98.7-99.7) |
| keylock_-8pct_10s | 99.6 (99.0-99.8) |
| demand_pcafeter_snr0_10s | 86.9 (84.7-88.9) |
| demand_pstation_snr0_10s | 86.4 (84.1-88.4) |
| room_rt400_drr5_clean_10s | 99.3 (98.6-99.7) |
| room_rt800_drr-5_clean_10s | 74.6 (71.8-77.2) |
| room_rt1500_drr-10_clean_10s | 27.4 (24.7-30.2) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 33.4 (30.5-36.4) |
| room_rt800_drr-5_keylock_+8pct_10s | 53.2 (50.1-56.3) |
| room_rt800_drr-5_speed_+8pct_10s | 38.9 (35.9-42.0) |

b2: t_abs 40, t_ratio 2.5, test FAR 0.16%, median postings scanned per query 218,328
