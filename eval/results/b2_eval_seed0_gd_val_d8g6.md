# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on val (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 98.2 (96.6-99.1) |
| speed_+1pct_10s | 97.2 (95.4-98.3) |
| speed_+8pct_10s | 96.2 (94.1-97.6) |
| speed_-8pct_10s | 92.8 (90.2-94.8) |
| keylock_+8pct_10s | 94.2 (91.8-95.9) |
| keylock_-8pct_10s | 95.0 (92.7-96.6) |
| demand_pcafeter_snr0_10s | 64.2 (59.9-68.3) |
| demand_pstation_snr0_10s | 69.4 (65.2-73.3) |
| room_rt400_drr5_clean_10s | 91.6 (88.8-93.7) |
| room_rt800_drr-5_clean_10s | 48.4 (44.0-52.8) |
| room_rt1500_drr-10_clean_10s | 15.8 (12.9-19.3) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 27.0 (23.3-31.1) |
| room_rt800_drr-5_keylock_+8pct_10s | 33.8 (29.8-38.1) |
| room_rt800_drr-5_speed_+8pct_10s | 28.0 (24.2-32.1) |

b2: t_abs 31, t_ratio 4.0, val FAR 0.10%, median postings scanned per query 14,114
