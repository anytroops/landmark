# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on val (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 99.4 (98.3-99.8) |
| speed_+1pct_10s | 99.2 (98.0-99.7) |
| speed_+8pct_10s | 99.0 (97.7-99.6) |
| speed_-8pct_10s | 99.0 (97.7-99.6) |
| keylock_+8pct_10s | 99.0 (97.7-99.6) |
| keylock_-8pct_10s | 99.0 (97.7-99.6) |
| demand_pcafeter_snr0_10s | 43.2 (38.9-47.6) |
| demand_pstation_snr0_10s | 56.0 (51.6-60.3) |
| room_rt400_drr5_clean_10s | 99.0 (97.7-99.6) |
| room_rt800_drr-5_clean_10s | 96.4 (94.4-97.7) |
| room_rt1500_drr-10_clean_10s | 56.2 (51.8-60.5) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 39.0 (34.8-43.3) |
| room_rt800_drr-5_keylock_+8pct_10s | 68.0 (63.8-71.9) |
| room_rt800_drr-5_speed_+8pct_10s | 72.6 (68.5-76.3) |

b2: t_abs 27, t_ratio 2.5, val FAR 0.06%, median postings scanned per query 145,598
