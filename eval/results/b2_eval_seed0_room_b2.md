# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on test (95% Wilson CI); thresholds from b2_eval_seed0_tn_b6x5.json.

| Condition | B2 |
|---|---|
| clean_10s | 99.9 (99.4-100.0) |
| room_rt400_drr5_clean_10s | 99.6 (99.0-99.8) |
| room_rt800_drr-5_clean_10s | 68.3 (65.4-71.1) |
| room_rt1500_drr-10_clean_10s | 14.3 (12.3-16.6) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 26.8 (24.1-29.6) |
| keylock_+8pct_10s | 99.7 (99.1-99.9) |
| room_rt800_drr-5_keylock_+8pct_10s | 35.4 (32.5-38.4) |

b2: t_abs 57, t_ratio 2.0, test FAR 0.09%, median postings scanned per query 215,150
