# B2 vs pairs, one catalog of 3000 fma_medium tracks

Accuracy at threshold on test (95% Wilson CI); thresholds from b2_eval_seed0_d37.json.

| Condition | Pairs | B2 |
|---|---|---|
| clean_10s | 99.9 (99.4-100.0) | 99.9 (99.4-100.0) |
| white_snr5_10s | 99.6 (99.0-99.8) | 98.4 (97.4-99.0) |
| white_snr0_10s | 98.5 (97.5-99.1) | 94.9 (93.4-96.1) |
| white_snr-5_10s | 94.4 (92.8-95.7) | 67.2 (64.2-70.0) |
| white_snr-10_10s | 67.1 (64.1-69.9) | 5.4 (4.2-7.0) |
| demand_pcafeter_snr5_10s | 99.4 (98.7-99.7) | 95.9 (94.5-97.0) |
| demand_pcafeter_snr0_10s | 97.5 (96.3-98.3) | 70.5 (67.6-73.2) |
| demand_pcafeter_snr-5_10s | 88.0 (85.8-89.9) | 14.3 (12.3-16.6) |
| demand_pcafeter_snr-10_10s | 60.4 (57.3-63.4) | 1.6 (1.0-2.6) |
| demand_presto_snr5_10s | 98.9 (98.0-99.4) | 96.1 (94.7-97.1) |
| demand_presto_snr0_10s | 94.2 (92.6-95.5) | 77.3 (74.6-79.8) |
| demand_presto_snr-5_10s | 70.4 (67.5-73.1) | 24.7 (22.1-27.5) |
| demand_presto_snr-10_10s | 17.9 (15.6-20.4) | 2.5 (1.7-3.7) |
| demand_pstation_snr5_10s | 99.6 (99.0-99.8) | 93.3 (91.6-94.7) |
| demand_pstation_snr0_10s | 98.5 (97.5-99.1) | 60.6 (57.5-63.6) |
| demand_pstation_snr-5_10s | 89.9 (87.9-91.6) | 4.4 (3.3-5.9) |
| demand_pstation_snr-10_10s | 64.2 (61.2-67.1) | 0.0 (0.0-0.4) |
| demand_straffic_snr5_10s | 99.7 (99.1-99.9) | 98.3 (97.3-98.9) |
| demand_straffic_snr0_10s | 99.3 (98.6-99.7) | 94.6 (93.0-95.8) |
| demand_straffic_snr-5_10s | 97.2 (96.0-98.1) | 77.3 (74.6-79.8) |
| demand_straffic_snr-10_10s | 90.2 (88.2-91.9) | 32.3 (29.5-35.3) |
| demand_spsquare_snr5_10s | 99.8 (99.3-99.9) | 99.0 (98.2-99.5) |
| demand_spsquare_snr0_10s | 99.3 (98.6-99.7) | 94.4 (92.8-95.7) |
| demand_spsquare_snr-5_10s | 97.7 (96.6-98.5) | 75.4 (72.6-78.0) |
| demand_spsquare_snr-10_10s | 91.6 (89.7-93.2) | 28.3 (25.6-31.2) |
| demand_tbus_snr5_10s | 99.9 (99.4-100.0) | 99.6 (99.0-99.8) |
| demand_tbus_snr0_10s | 99.9 (99.4-100.0) | 99.6 (99.0-99.8) |
| demand_tbus_snr-5_10s | 99.7 (99.1-99.9) | 98.5 (97.5-99.1) |
| demand_tbus_snr-10_10s | 98.6 (97.7-99.2) | 91.1 (89.2-92.7) |
| demand_tcar_snr5_10s | 99.9 (99.4-100.0) | 99.9 (99.4-100.0) |
| demand_tcar_snr0_10s | 99.9 (99.4-100.0) | 99.8 (99.3-99.9) |
| demand_tcar_snr-5_10s | 99.9 (99.4-100.0) | 99.7 (99.1-99.9) |
| demand_tcar_snr-10_10s | 99.9 (99.4-100.0) | 98.8 (97.9-99.3) |
| demand_tmetro_snr5_10s | 99.6 (99.0-99.8) | 99.0 (98.2-99.5) |
| demand_tmetro_snr0_10s | 99.1 (98.3-99.5) | 95.9 (94.5-97.0) |
| demand_tmetro_snr-5_10s | 97.2 (96.0-98.1) | 76.0 (73.3-78.5) |
| demand_tmetro_snr-10_10s | 89.6 (87.6-91.3) | 28.5 (25.8-31.4) |
| demand_dliving_snr5_10s | 99.9 (99.4-100.0) | 99.2 (98.4-99.6) |
| demand_dliving_snr0_10s | 99.5 (98.8-99.8) | 97.8 (96.7-98.5) |
| demand_dliving_snr-5_10s | 98.4 (97.4-99.0) | 87.1 (84.9-89.0) |
| demand_dliving_snr-10_10s | 94.6 (93.0-95.8) | 46.6 (43.5-49.7) |
| demand_npark_snr5_10s | 99.5 (98.8-99.8) | 98.5 (97.5-99.1) |
| demand_npark_snr0_10s | 98.4 (97.4-99.0) | 93.3 (91.6-94.7) |
| demand_npark_snr-5_10s | 92.8 (91.0-94.2) | 68.3 (65.4-71.1) |
| demand_npark_snr-10_10s | 71.5 (68.6-74.2) | 16.0 (13.9-18.4) |

pairs: t_abs 2, t_ratio 2.5, test FAR 0.11%, median postings scanned per query 77,567

b2: t_abs 44, t_ratio 2.5, test FAR 0.08%, median postings scanned per query 268,374
