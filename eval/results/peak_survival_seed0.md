# Triplet peak survival under noise (200 val positives)

Share of a query's peaks within 1 frame and 1 band of a peak the indexed track has at the same place.

| Condition | baseline | A 129 frames, 50th pct | B 6 slices x 5 |
|---|---|---|---|
| clean_10s | 90.6% | 79.9% | 86.0% |
| white_snr0_10s | 50.6% | 33.1% | 44.0% |
| demand_pstation_snr0_10s | 34.4% | 31.7% | 44.8% |
| demand_pcafeter_snr0_10s | 33.2% | 27.8% | 39.8% |
| demand_presto_snr0_10s | 34.9% | 24.8% | 34.9% |
| demand_dliving_snr-5_10s | 30.3% | 29.9% | 36.2% |
| demand_tcar_snr0_10s | 67.8% | 61.6% | 71.4% |

Share of query peaks per sixth of the bands, low to high (%).

| Option | Condition | 1 | 2 | 3 | 4 | 5 | 6 |
|---|---|---|---|---|---|---|---|
| baseline | clean_10s | 21.9 | 18.8 | 21.5 | 19.2 | 13.3 | 5.2 |
| baseline | white_snr0_10s | 27.5 | 21.4 | 22.8 | 18.1 | 8.7 | 1.5 |
| baseline | demand_pstation_snr0_10s | 34.3 | 24.3 | 22.1 | 14.1 | 4.6 | 0.6 |
| baseline | demand_pcafeter_snr0_10s | 30.8 | 22.3 | 24.0 | 16.0 | 5.8 | 1.1 |
| baseline | demand_presto_snr0_10s | 24.9 | 23.2 | 24.3 | 18.7 | 7.7 | 1.2 |
| baseline | demand_dliving_snr-5_10s | 31.1 | 20.3 | 23.6 | 18.4 | 5.6 | 1.1 |
| baseline | demand_tcar_snr0_10s | 29.0 | 20.2 | 20.2 | 17.0 | 10.4 | 3.2 |
| A 129 frames, 50th pct | clean_10s | 20.7 | 16.7 | 18.2 | 17.5 | 15.5 | 11.6 |
| A 129 frames, 50th pct | white_snr0_10s | 29.4 | 21.8 | 21.5 | 16.7 | 8.6 | 2.0 |
| A 129 frames, 50th pct | demand_pstation_snr0_10s | 27.3 | 20.1 | 18.3 | 13.6 | 11.2 | 9.6 |
| A 129 frames, 50th pct | demand_pcafeter_snr0_10s | 21.7 | 18.0 | 18.3 | 17.6 | 14.1 | 10.3 |
| A 129 frames, 50th pct | demand_presto_snr0_10s | 22.7 | 18.7 | 18.8 | 17.9 | 13.7 | 8.3 |
| A 129 frames, 50th pct | demand_dliving_snr-5_10s | 24.4 | 18.0 | 17.6 | 16.9 | 13.5 | 9.6 |
| A 129 frames, 50th pct | demand_tcar_snr0_10s | 22.6 | 17.3 | 17.7 | 16.8 | 14.7 | 11.0 |
| B 6 slices x 5 | clean_10s | 16.2 | 16.1 | 17.2 | 17.4 | 17.2 | 15.9 |
| B 6 slices x 5 | white_snr0_10s | 16.7 | 16.4 | 16.6 | 16.7 | 16.8 | 16.8 |
| B 6 slices x 5 | demand_pstation_snr0_10s | 17.0 | 16.8 | 16.8 | 16.7 | 16.7 | 15.9 |
| B 6 slices x 5 | demand_pcafeter_snr0_10s | 17.0 | 16.7 | 16.8 | 16.7 | 16.6 | 16.2 |
| B 6 slices x 5 | demand_presto_snr0_10s | 16.7 | 16.7 | 16.8 | 16.7 | 16.7 | 16.5 |
| B 6 slices x 5 | demand_dliving_snr-5_10s | 17.0 | 16.4 | 16.9 | 16.9 | 16.7 | 16.2 |
| B 6 slices x 5 | demand_tcar_snr0_10s | 17.4 | 16.9 | 16.9 | 16.8 | 16.7 | 15.3 |

Commit 70c55ab, dirty False.
