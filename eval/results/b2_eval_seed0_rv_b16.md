# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on test (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 99.8 (99.3-99.9) |
| speed_+1pct_10s | 99.8 (99.3-99.9) |
| speed_+8pct_10s | 99.5 (98.8-99.8) |
| speed_-8pct_10s | 98.8 (97.9-99.3) |
| keylock_+8pct_10s | 98.6 (97.7-99.2) |
| keylock_-8pct_10s | 98.7 (97.8-99.2) |
| demand_pcafeter_snr0_10s | 44.6 (41.5-47.7) |
| demand_pstation_snr0_10s | 53.6 (50.5-56.7) |
| room_rt400_drr5_clean_10s | 99.7 (99.1-99.9) |
| room_rt800_drr-5_clean_10s | 93.0 (91.2-94.4) |
| room_rt1500_drr-10_clean_10s | 56.6 (53.5-59.6) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 41.5 (38.5-44.6) |
| room_rt800_drr-5_keylock_+8pct_10s | 64.8 (61.8-67.7) |
| room_rt800_drr-5_speed_+8pct_10s | 71.4 (68.5-74.1) |

b2: t_abs 27, t_ratio 2.5, test FAR 0.21%, median postings scanned per query 147,149
