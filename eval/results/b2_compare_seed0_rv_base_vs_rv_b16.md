# Triplets _rv_b16 vs _rv_base, test split, exact McNemar

Each run at its own val-calibrated thresholds; the same test excerpts.

| Condition | n | A | B | right only with B | right only with A | p |
|---|---|---|---|---|---|---|
| clean_10s | 1000 | 99.9 | 99.8 | 0 | 1 | 1 |
| speed_+1pct_10s | 1000 | 99.9 | 99.8 | 0 | 1 | 1 |
| speed_+8pct_10s | 1000 | 99.4 | 99.5 | 5 | 4 | 1 |
| speed_-8pct_10s | 1000 | 98.8 | 98.8 | 10 | 10 | 1 |
| keylock_+8pct_10s | 1000 | 99.4 | 98.6 | 3 | 11 | 0.0574 |
| keylock_-8pct_10s | 1000 | 99.6 | 98.7 | 1 | 10 | 0.0117 |
| demand_pcafeter_snr0_10s | 1000 | 86.9 | 44.6 | 2 | 425 | 5.27e-124 |
| demand_pstation_snr0_10s | 1000 | 86.4 | 53.6 | 6 | 334 | 1.87e-90 |
| room_rt400_drr5_clean_10s | 1000 | 99.3 | 99.7 | 6 | 2 | 0.289 |
| room_rt800_drr-5_clean_10s | 1000 | 74.6 | 93.0 | 214 | 30 | 2.02e-35 |
| room_rt1500_drr-10_clean_10s | 1000 | 27.4 | 56.6 | 367 | 75 | 2.9e-47 |
| room_rt800_drr-5_demand_pcafeter_snr5_10s | 1000 | 33.4 | 41.5 | 214 | 133 | 1.61e-05 |
| room_rt800_drr-5_keylock_+8pct_10s | 1000 | 53.2 | 64.8 | 237 | 121 | 8.74e-10 |
| room_rt800_drr-5_speed_+8pct_10s | 1000 | 38.9 | 71.4 | 387 | 62 | 1.72e-58 |

A (_rv_base): t_abs 40, t_ratio 2.5, test FAR 0.16%, mean real noise 86.65, mean clean and tempo 99.50
B (_rv_b16): t_abs 27, t_ratio 2.5, test FAR 0.21%, mean real noise 49.10, mean clean and tempo 99.20
