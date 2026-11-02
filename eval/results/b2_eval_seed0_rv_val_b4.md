# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on val (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 99.4 (98.3-99.8) |
| speed_+1pct_10s | 99.0 (97.7-99.6) |
| speed_+8pct_10s | 98.8 (97.4-99.4) |
| speed_-8pct_10s | 98.8 (97.4-99.4) |
| keylock_+8pct_10s | 98.6 (97.1-99.3) |
| keylock_-8pct_10s | 99.0 (97.7-99.6) |
| demand_pcafeter_snr0_10s | 37.8 (33.7-42.1) |
| demand_pstation_snr0_10s | 42.6 (38.3-47.0) |
| room_rt400_drr5_clean_10s | 99.0 (97.7-99.6) |
| room_rt800_drr-5_clean_10s | 81.6 (78.0-84.8) |
| room_rt1500_drr-10_clean_10s | 23.4 (19.9-27.3) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 26.0 (22.3-30.0) |
| room_rt800_drr-5_keylock_+8pct_10s | 48.6 (44.2-53.0) |
| room_rt800_drr-5_speed_+8pct_10s | 47.6 (43.3-52.0) |

b2: t_abs 33, t_ratio 2.5, val FAR 0.09%, median postings scanned per query 139,628
