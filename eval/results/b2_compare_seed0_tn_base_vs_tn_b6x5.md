# Triplets _tn_b6x5 vs _tn_base, test split, exact McNemar

Each run at its own val-calibrated thresholds; the same test excerpts.

| Condition | n | A | B | right only with B | right only with A | p |
|---|---|---|---|---|---|---|
| clean_10s | 1000 | 99.9 | 99.9 | 0 | 0 | 1 |
| white_snr0_10s | 1000 | 95.3 | 93.6 | 6 | 23 | 0.00232 |
| white_snr-5_10s | 1000 | 70.5 | 73.6 | 89 | 58 | 0.0131 |
| demand_pcafeter_snr0_10s | 1000 | 73.5 | 86.5 | 149 | 19 | 3.33e-26 |
| demand_pcafeter_snr-5_10s | 1000 | 14.3 | 44.6 | 316 | 13 | 1.27e-76 |
| demand_presto_snr0_10s | 1000 | 78.3 | 83.2 | 93 | 44 | 3.44e-05 |
| demand_presto_snr-5_10s | 1000 | 23.7 | 29.4 | 134 | 77 | 0.000106 |
| demand_pstation_snr0_10s | 1000 | 66.4 | 88.3 | 227 | 8 | 7.68e-57 |
| demand_pstation_snr-5_10s | 1000 | 5.6 | 52.0 | 469 | 5 | 8.09e-132 |
| demand_straffic_snr0_10s | 1000 | 94.9 | 97.3 | 27 | 3 | 8.43e-06 |
| demand_straffic_snr-5_10s | 1000 | 79.1 | 88.7 | 104 | 8 | 1.98e-22 |
| demand_spsquare_snr0_10s | 1000 | 95.7 | 97.1 | 18 | 4 | 0.00434 |
| demand_spsquare_snr-5_10s | 1000 | 80.0 | 88.8 | 96 | 8 | 2.77e-20 |
| demand_tbus_snr0_10s | 1000 | 99.7 | 99.8 | 1 | 0 | 1 |
| demand_tbus_snr-5_10s | 1000 | 98.6 | 99.3 | 7 | 0 | 0.0156 |
| demand_tcar_snr0_10s | 1000 | 99.8 | 99.9 | 1 | 0 | 1 |
| demand_tcar_snr-5_10s | 1000 | 99.5 | 99.7 | 2 | 0 | 0.5 |
| demand_tmetro_snr0_10s | 1000 | 96.7 | 97.6 | 16 | 7 | 0.0931 |
| demand_tmetro_snr-5_10s | 1000 | 78.7 | 90.2 | 127 | 12 | 2.11e-25 |
| demand_dliving_snr0_10s | 1000 | 98.0 | 98.5 | 11 | 6 | 0.332 |
| demand_dliving_snr-5_10s | 1000 | 88.2 | 94.3 | 74 | 13 | 1.59e-11 |
| demand_npark_snr0_10s | 1000 | 93.9 | 93.8 | 10 | 11 | 1 |
| demand_npark_snr-5_10s | 1000 | 71.1 | 73.3 | 60 | 38 | 0.0334 |
| speed_+1pct_10s | 1000 | 99.9 | 99.9 | 0 | 0 | 1 |
| speed_+8pct_10s | 1000 | 99.9 | 99.8 | 0 | 1 | 1 |
| speed_-8pct_10s | 1000 | 99.9 | 99.5 | 0 | 4 | 0.125 |
| keylock_+8pct_10s | 1000 | 99.9 | 99.7 | 0 | 2 | 0.5 |
| keylock_-8pct_10s | 1000 | 99.8 | 99.7 | 0 | 1 | 1 |

A (_tn_base): t_abs 55, t_ratio 2.0, test FAR 0.16%, mean real noise 76.78, mean clean and tempo 99.88
B (_tn_b6x5): t_abs 57, t_ratio 2.0, test FAR 0.19%, mean real noise 85.11, mean clean and tempo 99.75
