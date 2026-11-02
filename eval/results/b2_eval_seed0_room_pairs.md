# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on test (95% Wilson CI); thresholds from b2_eval_seed0_d37_demand.json.

| Condition | Pairs |
|---|---|
| clean_10s | 99.9 (99.4-100.0) |
| room_rt400_drr5_clean_10s | 99.9 (99.4-100.0) |
| room_rt800_drr-5_clean_10s | 98.2 (97.2-98.9) |
| room_rt1500_drr-10_clean_10s | 66.5 (63.5-69.4) |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 88.8 (86.7-90.6) |
| keylock_+8pct_10s | 99.7 (99.1-99.9) |
| room_rt800_drr-5_keylock_+8pct_10s | 6.4 (5.0-8.1) |

pairs: t_abs 2, t_ratio 2.5, test FAR 0.06%, median postings scanned per query 77,568
