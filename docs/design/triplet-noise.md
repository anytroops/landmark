# Triplet fingerprint under real noise: options, measured before building

Status: step 1 done (2026-10-31): B chosen on val, A failed the rule. Step 3 (full val and test) not yet run. Sean's pick of what to test: A, B, and A with B.

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

### Why (200 val positives, scratch analysis, not a committed run)

Share of a noisy query's peaks within 1 frame and 1 band of a peak the indexed track has at the same place (reference with context on both sides, as indexed):

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

### Step 3: not yet run

Full val and test (1,000 positives, 500 negatives per split) for the baseline and B, with `python -m eval.b2_compare pair _tn_base _tn_b6x5` for the McNemar table, plus an exploratory val run of B with the least bad A (129 frames, 50th percentile) since Sean asked about a combination. Started 2026-10-31 18:20 and stopped at 18:37 before any run finished (laptop shut down); rerun from scratch.
