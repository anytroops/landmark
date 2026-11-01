# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on test (95% Wilson CI); thresholds calibrated on val.

| Condition | B2 |
|---|---|
| clean_10s | 99.9 (99.4-100.0) |
| white_snr0_10s | 95.3 (93.8-96.4) |
| white_snr-5_10s | 70.5 (67.6-73.2) |
| demand_pcafeter_snr0_10s | 73.5 (70.7-76.1) |
| demand_pcafeter_snr-5_10s | 14.3 (12.3-16.6) |
| demand_presto_snr0_10s | 78.3 (75.6-80.7) |
| demand_presto_snr-5_10s | 23.7 (21.2-26.4) |
| demand_pstation_snr0_10s | 66.4 (63.4-69.3) |
| demand_pstation_snr-5_10s | 5.6 (4.3-7.2) |
| demand_straffic_snr0_10s | 94.9 (93.4-96.1) |
| demand_straffic_snr-5_10s | 79.1 (76.5-81.5) |
| demand_spsquare_snr0_10s | 95.7 (94.3-96.8) |
| demand_spsquare_snr-5_10s | 80.0 (77.4-82.4) |
| demand_tbus_snr0_10s | 99.7 (99.1-99.9) |
| demand_tbus_snr-5_10s | 98.6 (97.7-99.2) |
| demand_tcar_snr0_10s | 99.8 (99.3-99.9) |
| demand_tcar_snr-5_10s | 99.5 (98.8-99.8) |
| demand_tmetro_snr0_10s | 96.7 (95.4-97.6) |
| demand_tmetro_snr-5_10s | 78.7 (76.1-81.1) |
| demand_dliving_snr0_10s | 98.0 (96.9-98.7) |
| demand_dliving_snr-5_10s | 88.2 (86.1-90.1) |
| demand_npark_snr0_10s | 93.9 (92.2-95.2) |
| demand_npark_snr-5_10s | 71.1 (68.2-73.8) |
| speed_+1pct_10s | 99.9 (99.4-100.0) |
| speed_+8pct_10s | 99.9 (99.4-100.0) |
| speed_-8pct_10s | 99.9 (99.4-100.0) |
| keylock_+8pct_10s | 99.9 (99.4-100.0) |
| keylock_-8pct_10s | 99.8 (99.3-99.9) |

b2: t_abs 55, t_ratio 2.0, test FAR 0.16%, median postings scanned per query 267,410
