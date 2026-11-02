# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on val (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 99.8 (98.9-100.0) |
| speed_+1pct_10s | 99.0 (97.7-99.6) |
| speed_+8pct_10s | 98.4 (96.9-99.2) |
| speed_-8pct_10s | 98.8 (97.4-99.4) |
| keylock_+8pct_10s | 98.8 (97.4-99.4) |
| keylock_-8pct_10s | 98.2 (96.6-99.1) |
| demand_pcafeter_snr0_10s | 38.2 (34.0-42.5) |
| demand_pstation_snr0_10s | 45.2 (40.9-49.6) |
| room_rt400_drr5_clean_10s | 99.2 (98.0-99.7) |
| room_rt800_drr-5_clean_10s | 90.2 (87.3-92.5) |
| room_rt1500_drr-10_clean_10s | 37.6 (33.5-41.9) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 30.8 (26.9-35.0) |
| room_rt800_drr-5_keylock_+8pct_10s | 55.6 (51.2-59.9) |
| room_rt800_drr-5_speed_+8pct_10s | 60.6 (56.3-64.8) |

b2: t_abs 2, t_ratio 3.0, val FAR 0.10%, median postings scanned per query 143,555
