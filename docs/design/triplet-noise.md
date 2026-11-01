# Triplet fingerprint under real noise: options, measured before building

Status: measured (2026-10-31). B wins on test; A failed on val, and A with B is worse than B alone. Building B in Go and re-indexing waits on Sean's pick. Sean's pick of what to test: A, B, and A with B.

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

`eval.b2_eval` (Python prototype, in-memory catalog of 3,000 fma_medium tracks, the same for every run), triplets only (`--systems b2`), options set with `--b2-params` for the index and the queries alike. Conditions, the same list in every run so every run gets the same excerpts and noise windows: clean; white noise at 0 and -5 dB; the ten DEMAND places at 0 and -5 dB; turntable +1%, +8%, -8%; key lock +8%, -8% (28). Val queries draw noise from the first half of each recording, test queries from the second (D52).

1. **Choose each option's setting on val** (500 positives and 500 negatives; thresholds calibrated on val as D13: pooled false accepts at most 0.1%, accuracy objective over the non-tempo conditions). Grid: A with windows of 43 and 129 frames (1 and 3 s) and the 25th and 50th percentiles; B with 6 slices of at most 5 or 10 peaks, and 3 slices of at most 15. Today's triplets on the same grid run as the baseline.
2. **Rule, fixed before the runs:** for each option, the setting with the highest mean val accuracy over the 20 real-noise conditions, among settings whose mean over clean and the five tempo conditions is at most 0.5 points below the baseline's. If none qualifies, the option fails. A with B uses the chosen A and B settings.
3. **Then val and test in full** (1,000 positives and 500 negatives per split) for the baseline, A, B, and A with B, each calibrated on its own val; test reported, with exact McNemar tests against the baseline on the same excerpts.

## Results

### Step 1: settings on val

`eval/results/b2_eval_seed0_tn_val_{tag}.*` (commit c96eede, clean tree), rule applied with `python -m eval.b2_compare grid`. Mean val accuracy in points; each run calibrated on its own val (pooled FAR at most 0.1%).

| Setting | 20 real-noise conditions | Clean and 5 tempo conditions | Qualifies |
|---|---|---|---|
| Baseline (today) | 76.25 | 99.77 | |
| B, 6 slices, at most 5 | **86.01** | 99.67 | yes, chosen |
| B, 6 slices, at most 10 | 76.27 | 99.77 | yes |
| B, 3 slices, at most 15 | 77.75 | 99.77 | yes |
| A, 43 frames, 25th pct | 46.83 | 98.57 | no |
| A, 43 frames, 50th pct | 56.09 | 98.60 | no |
| A, 129 frames, 25th pct | 59.05 | 98.97 | no |
| A, 129 frames, 50th pct | 64.10 | 99.17 | no |

- **B (6 slices, at most 5)** gains most where today's triplets are weakest, at -5 dB: cafeteria 4.4% to 44.0%, train station 7.6% to 57.6%, living room 52.2% to 82.6%, metro 85.0% to 93.8%. White noise at 0 dB 96.6% to 95.2%. The looser caps change almost nothing, so the gain comes from forcing an even spread (6 x 5 = all 30 slots).
- **A fails at every setting**: worse under every noise (white noise at 0 dB 96.6% to between 14.6% and 56.0%) and more than 0.5 points below the baseline on clean and tempo. With no A chosen, the rule leaves no A with B to run.

### Why

`python -m eval.peak_survival` (200 val positives, the eval's excerpts and val noise draws; commit 70c55ab, clean tree; `eval/results/peak_survival_seed0.*`). Share of a noisy query's peaks within 1 frame and 1 band of a peak the indexed track has at the same place (reference with 160 frames of context on each side, as indexed):

| Condition | Baseline | A (129, 50th) | B (6 x 5) |
|---|---|---|---|
| clean | 90.6% | 79.9% | 86.0% |
| white 0 dB | 50.6% | 33.1% | 44.0% |
| train station 0 dB | 34.4% | 31.7% | 44.8% |
| cafeteria 0 dB | 33.2% | 27.8% | 39.8% |
| restaurant 0 dB | 34.9% | 24.8% | 34.9% |
| living room -5 dB | 30.3% | 29.9% | 36.2% |
| car 0 dB | 67.8% | 61.6% | 71.4% |

Under noise the baseline's peaks move down: the top third of the bands holds 18.5% of a clean query's peaks but 5.2% at a train station at 0 dB, so the high-band music peaks the index has are lost. B keeps about a sixth in each slice in every condition. A matches fewer peaks even in clean audio (79.9%), the edge effect named under Options: a 10 s query's normalization near its ends differs from the indexed track's.

### Step 3: test

`eval/results/b2_eval_seed0_tn_{base,b6x5}.*` (commit 846d864, clean tree, AC power): 1,000 positives and 500 negatives per split, 28 conditions, each run calibrated on its own val; `python -m eval.b2_compare pair _tn_base _tn_b6x5` gives `b2_compare_seed0_tn_base_vs_tn_b6x5.*`. Accuracy at threshold on test, then the queries right only with B and only with the baseline, and the exact McNemar p:

| Condition | Baseline | B (6 x 5) | Only B / only baseline | p |
|---|---|---|---|---|
| Clean | 99.9 | 99.9 | 0 / 0 | 1 |
| White, 0 dB | 95.3 | 93.6 | 6 / 23 | 0.002 |
| White, -5 dB | 70.5 | 73.6 | 89 / 58 | 0.013 |
| Train station, 0 dB | 66.4 | 88.3 | 227 / 8 | 8e-57 |
| Train station, -5 dB | 5.6 | 52.0 | 469 / 5 | 8e-132 |
| Cafeteria, 0 dB | 73.5 | 86.5 | 149 / 19 | 3e-26 |
| Cafeteria, -5 dB | 14.3 | 44.6 | 316 / 13 | 1e-76 |
| Restaurant, 0 dB | 78.3 | 83.2 | 93 / 44 | 3e-05 |
| Restaurant, -5 dB | 23.7 | 29.4 | 134 / 77 | 1e-04 |
| Park, 0 dB | 93.9 | 93.8 | 10 / 11 | 1 |
| Park, -5 dB | 71.1 | 73.3 | 60 / 38 | 0.033 |
| Public square, 0 dB | 95.7 | 97.1 | 18 / 4 | 0.004 |
| Public square, -5 dB | 80.0 | 88.8 | 96 / 8 | 3e-20 |
| Street, 0 dB | 94.9 | 97.3 | 27 / 3 | 8e-06 |
| Street, -5 dB | 79.1 | 88.7 | 104 / 8 | 2e-22 |
| Subway train, 0 dB | 96.7 | 97.6 | 16 / 7 | 0.093 |
| Subway train, -5 dB | 78.7 | 90.2 | 127 / 12 | 2e-25 |
| Living room, 0 dB | 98.0 | 98.5 | 11 / 6 | 0.33 |
| Living room, -5 dB | 88.2 | 94.3 | 74 / 13 | 2e-11 |
| Bus, 0 dB | 99.7 | 99.8 | 1 / 0 | 1 |
| Bus, -5 dB | 98.6 | 99.3 | 7 / 0 | 0.016 |
| Car, 0 dB | 99.8 | 99.9 | 1 / 0 | 1 |
| Car, -5 dB | 99.5 | 99.7 | 2 / 0 | 0.5 |
| Turntable +1%, +8%, -8% | 99.9, 99.9, 99.9 | 99.9, 99.8, 99.5 | 0 / 5 in all | 0.125 at -8% |
| Key lock +8%, -8% | 99.9, 99.8 | 99.7, 99.7 | 0 / 3 in all | 0.5, 1 |

- **B is better under real noise.** Mean over the 20 real-noise conditions 76.78 to 85.11 points (val 76.25 to 86.01). Significantly better (p < 0.01) in 12 of the 20, worse in none. The largest gains are where the baseline was weakest: train station at -5 dB 5.6% to 52.0%, cafeteria at -5 dB 14.3% to 44.6%.
- **What it costs.** White noise at 0 dB 95.3% to 93.6% (p 0.002), the one significant loss. Clean and the five tempo conditions 99.88 to 99.75 points (none significant; 8 queries right only with the baseline, none only with B).
- **False accepts unchanged.** 23 (baseline) and 26 (B) of 14,000 test negatives, only one shared; paired p 0.77. Both are above the 0.1% the val calibration targets (13 and 14 of 14,000 on val), as the same thresholds see a different set of negatives on test.
- **Cheaper to search.** Median postings scanned per query 267,410 to 222,001 (fewer peaks where a slice of the bands has little), the index 22.5M to 22.0M postings.
- **Still behind the pairs.** The D52 run (same catalog, other noise draws, so not a paired comparison) had the pairs at 98.5% in a train station at 0 dB and 89.9% at -5 dB; B's 88.3% and 52.0% close part of that gap, not all of it.

### A with B (exploratory, val only)

Not run by the rule (no A setting qualified); run because Sean asked about a combination, with the least bad A (129 frames, 50th percentile) and B (6 x 5), 500 positives and 500 negatives (`b2_eval_seed0_tn_val_a129p50_b6x5.*`). Mean over the 20 real-noise conditions 69.80 points: better than that A alone (64.10), worse than the baseline (76.25) and far worse than B alone (86.01). White noise at 0 dB 60.2% (B alone 95.2%). A's normalization undoes what B gains.

## Conclusion

B (at most 5 of each second's 30 peaks in each sixth of the bands) is the fix to build, if Sean picks it. Building it means: the per-slice cap in Go's triplet peak picking with Python parity tests and golden vectors; re-indexing the triplet index; and before shipping, rerunning the DJ evals (synthetic mixes and UnmixDB, D36 and D37) and the throughput benchmark, since every triplet search changes. Nothing in the hash, segment format, or search changes.
