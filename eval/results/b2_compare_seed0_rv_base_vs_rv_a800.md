# Triplets _rv_a800 vs _rv_base, test split, exact McNemar

Each run at its own val-calibrated thresholds; the same test excerpts.

| Condition | n | A | B | right only with B | right only with A | p |
|---|---|---|---|---|---|---|
| clean_10s | 1000 | 99.9 | 99.9 | 0 | 0 | 1 |
| speed_+1pct_10s | 1000 | 99.9 | 99.9 | 0 | 0 | 1 |
| speed_+8pct_10s | 1000 | 99.4 | 99.2 | 0 | 2 | 0.5 |
| speed_-8pct_10s | 1000 | 98.8 | 98.2 | 2 | 8 | 0.109 |
| keylock_+8pct_10s | 1000 | 99.4 | 99.2 | 1 | 3 | 0.625 |
| keylock_-8pct_10s | 1000 | 99.6 | 99.2 | 1 | 5 | 0.219 |
| demand_pcafeter_snr0_10s | 1000 | 86.9 | 83.1 | 14 | 52 | 2.82e-06 |
| demand_pstation_snr0_10s | 1000 | 86.4 | 83.9 | 14 | 39 | 0.000802 |
| room_rt400_drr5_clean_10s | 1000 | 99.3 | 99.3 | 1 | 1 | 1 |
| room_rt800_drr-5_clean_10s | 1000 | 74.6 | 76.3 | 71 | 54 | 0.152 |
| room_rt1500_drr-10_clean_10s | 1000 | 27.4 | 31.0 | 96 | 60 | 0.00491 |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 1000 | 33.4 | 33.9 | 82 | 77 | 0.751 |
| room_rt800_drr-5_keylock_+8pct_10s | 1000 | 53.2 | 55.5 | 89 | 66 | 0.0769 |
| room_rt800_drr-5_speed_+8pct_10s | 1000 | 38.9 | 42.2 | 99 | 66 | 0.0125 |

A (_rv_base): t_abs 40, t_ratio 2.5, test FAR 0.16%, mean real noise 86.65, mean clean and tempo 99.50
B (_rv_a800): t_abs 39, t_ratio 2.5, test FAR 0.10%, mean real noise 83.50, mean clean and tempo 99.27
