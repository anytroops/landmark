# Triplet fingerprint under real noise: options, measured before building

Status: experiment running (2026-10-31), Sean's pick of what to test: A, B, and A with B.

## Problem

Under recorded background noise (D52; EVAL, "The triplet fingerprint under real noise"), the triplet fingerprint is significantly worse than the pair fingerprint in 36 of 44 noisy cells and better in none: at 0 dB a train station leaves it 60.6% (pairs 98.5%), a cafeteria 70.5% (97.5%). A song played at its own speed is still answered by the pair search, but a tempo-changed song in a noisy room (a DJ set recorded on a phone) depends on the triplets.

Peaks today (D9, D33): local maxima of the log-frequency spectrogram over a 17-frame x 9-band neighborhood, above an absolute floor, then the strongest 30 per second. Noise that is loud in some bands wins those 30 slots from the music.

## Options

- **A. Per-band normalization.** Find and rank peaks on the spectrogram less each band's running percentile over a window of frames (the noise floor, roughly), so a steady noise stops looking like strong peaks. The absolute floor still applies to the spectrogram itself. Parameters: window (`norm_frames`) and percentile (`norm_pct`). Cost: a sliding percentile per band. Risk: it also flattens sustained notes, and a 10 s query's first and last half-window see less context than the indexed track does at the same place.
- **B. Peaks spread across frequency.** Split the bands into equal slices and let each keep at most a number of a second's 30 peaks (`peak_regions`, `region_cap`), so noise loud in one range cannot take every slot. Cost: none worth measuring. Risk: fewer peaks where the music is, in clean audio.
- **A with B.** Both.
- Not tested, as discussed with Sean: raising the filterbank's 55 Hz floor (the band shares did not explain the bad cases, and music has 45% of its power from 55 to 300 Hz); changing nothing and leaning on the pair search (leaves tempo-changed songs in noise weak).

All of these change the triplet fingerprint, so a winner means re-indexing the triplet index and porting the change to Go with parity tests. Nothing in the hash, segment format, or search changes.

## Method

`eval.b2_eval` (Python prototype, in-memory catalog of 3,000 fma_medium tracks, the same for every run), triplets only (`--systems b2`), options set with `--b2-params` for the index and the queries alike. Conditions, the same list in every run so every run gets the same excerpts and noise windows: clean; white noise at 0 and -5 dB; the ten DEMAND places at 0 and -5 dB; turntable +1%, +8%, -8%; key lock +8%, -8% (27). Val queries draw noise from the first half of each recording, test queries from the second (D52).

1. **Choose each option's setting on val** (500 positives and 500 negatives; thresholds calibrated on val as D13: pooled false accepts at most 0.1%, accuracy objective over the non-tempo conditions). Grid: A with windows of 43 and 129 frames (1 and 3 s) and the 25th and 50th percentiles; B with 6 slices of at most 5 or 10 peaks, and 3 slices of at most 15. Today's triplets on the same grid run as the baseline.
2. **Rule, fixed before the runs:** for each option, the setting with the highest mean val accuracy over the 20 real-noise conditions, among settings whose mean over clean and the five tempo conditions is at most 0.5 points below the baseline's. If none qualifies, the option fails. A with B uses the chosen A and B settings.
3. **Then val and test in full** (1,000 positives and 500 negatives per split) for the baseline, A, B, and A with B, each calibrated on its own val; test reported, with exact McNemar tests against the baseline on the same excerpts.

## Results

[TBD]
