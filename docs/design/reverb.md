# Reverberation: options, measured before building

Status: A, B, and gated B tested (2026-11-02): nothing worth building; B alone helps reverberation and costs noise. Next: check the simulation against a real phone recording.

## Problem

A DJ set recorded on a phone hears the room as well as the music. In a simulated recording of the UnmixDB test mixes (`eval.room_mixes`: a synthetic room impulse response, `degrade.rir`, and a phone microphone's 150 Hz high-pass), tracklisting found:

| Recording | Songs found (of 1,135) | Precision |
|---|---|---|
| Clean | 1,081 (95.2%) | 98.6% |
| Phone microphone only | 1,088 (95.9%) | 99.4% |
| Room only: 0.8 s reverberation, phone 5 dB below the reverb | 772 (68.0%) | 89.4% |
| That room and cafeteria crowd at +5 dB | 615 (54.2%) | 93.1% |
| Crowd only (cafeteria at +5 dB) | 912 (80.4%) | 98.7% |
| Milder room: 0.4 s, phone 5 dB above the reverb | 1,001 (88.2%) | 96.5% |

(`eval/results/local/room_*.json`, commits 61cd7f3 to ee2b6fe; clean is `runs_d55`, scored the same way.) By the songs' tempo change, songs found:

| Recording | Unchanged | Turntable | Key lock |
|---|---|---|---|
| Clean | 383/384 (99.7%) | 330/367 (89.9%) | 368/384 (95.8%) |
| Phone microphone only | 384 (100%) | 333 (90.7%) | 371 (96.6%) |
| Crowd only | 371 (96.6%) | 235 (64.0%) | 306 (79.7%) |
| Milder room | 380 (99.0%) | 284 (77.4%) | 337 (87.8%) |
| Room, 0.8 s | 333 (86.7%) | 203 (55.3%) | 236 (61.5%) |
| Room and crowd | 271 (70.6%) | 158 (43.1%) | 186 (48.4%) |

Songs at their own tempo are carried by the pair search and hold up best; tempo-changed songs depend on the triplets. Single 10 s clips on `eval.b2_eval`'s 3,000-track catalog, 1,000 positives per condition, each fingerprint at its own published thresholds (triplets D53's, t_abs 57, t_ratio 2.0; pairs D37's, 2, 2.5; `b2_eval_seed0_room_{b2,pairs}.*`):

| Condition | Triplets | Pairs |
|---|---|---|
| Clean | 99.9% | 99.9% |
| Room 0.4 s, phone 5 dB above the reverb | 99.6% | 99.9% |
| Room 0.8 s, 5 dB below | 68.3% | 98.2% |
| Room 1.5 s, 10 dB below | 14.3% | 66.5% |
| Room 0.8 s and crowd at +5 dB | 26.8% | 88.8% |
| Key lock +8% | 99.7% | 99.7% |
| Room 0.8 s and key lock +8% | 35.4% | 6.4% |

Test false accepts 0.09% (triplets) and 0.06% (pairs) over all conditions. The triplets are the fragile fingerprint under reverberation, as under noise (D52): at 0.8 s they keep 68.3% where the pairs keep 98.2%, and a tempo-changed song in that room is found by neither most of the time.

Why, as a hypothesis (not measured): reverberation smears each note forward in time: a peak's energy lingers in its band for hundreds of milliseconds, and the tails of earlier notes become local maxima of their own. Both fingerprints pick peaks by magnitude in a neighborhood, so the tails take peak slots, and the hashes made from tails do not match the dry song in the index.

The simulation is a statistical room (exponentially decaying noise, no early reflections, the same decay at every frequency), and it does not model the speaker or a phone's own processing (automatic gain, noise suppression). A phone recording of mixes played through speakers would check it.

## Options

- **A. Subtract the estimated late reverberation from the query** (spectral subtraction of late reverb, as in Lebart, Boucher, and Denbigh 2001): the late reverberation's power is predicted from the query's own power spectrogram some 50 ms earlier, decayed at a reverberation time estimated from the query, and subtracted, with a floor, before peaks are picked. Query side only, so no index is rebuilt. Risks: a wrong reverberation estimate removes music; it must cost nothing on dry queries.
- **B. Rank peaks by onset contrast** (index and query): a candidate peak's rank is its height above the same band's maximum over the preceding frames, so a tail decaying from earlier energy ranks low while an onset, whose direct sound arrives first, ranks high. Changes the fingerprint, so every index is rebuilt (as D53).
- **C. Longer windows when the room is reverberant:** more evidence per decision (15 or 20 s instead of 10 s), at the cost of detection delay and of short songs.
- **D. A learned front end** (with milestone 3's ML): a model trained to map reverberant spectrograms to dry ones, or to pick dry-robust peaks. The most work, and the training data would be this simulation's.

Each is measured as D53 was: settings chosen on val by a rule fixed before the runs, then test, single clips and DJ mixes, with clean and tempo conditions guarding against regressions.

## Method (fixed before the runs, 2026-11-02; Sean: test A and B)

`eval.b2_eval`, triplets only, the 3,000-track catalog, options set with `--b2-params` (A, `dereverb_rt60`, applies to queries only; B, `onset_frames`, to the index and queries). Conditions, the same list in every run: clean; turntable +1%, +8%, -8%; key lock +8%, -8%; cafeteria and train station at 0 dB (D53's gains, guarded); and six reverberant ones: rooms of 0.4 s (phone 5 dB above the reverb), 0.8 s (5 dB below), and 1.5 s (10 dB below), the 0.8 s room with cafeteria crowd at +5 dB, and the 0.8 s room with key lock +8% and with turntable +8% (14 in all).

1. **Settings on val** (500 positives and 500 negatives, thresholds calibrated on val as D13): A with an assumed reverberation time of 0.4, 0.8, and 1.5 s (late reverb 2 frames after its sound, at most 10 dB removed); B with the rise measured over 4, 8, and 16 frames (93 to 372 ms); and the baseline (D53).
2. **Rule:** for each option, the setting with the highest mean val accuracy over the six reverberant conditions, among settings whose mean over clean and the five tempo conditions is at most 0.5 points below the baseline's and whose mean over the two noise conditions is at most 1 point below (`python -m eval.b2_compare grid --target room --noise-margin 1`). If none qualifies, the option fails. A with B uses the chosen settings, run on val too.
3. **Then val and test in full** (1,000 positives and 500 negatives per split) for the baseline and each option that qualified, and A with B, with exact McNemar tests against the baseline on the same excerpts; then, for a winner, the DJ mixes in the simulated rooms once it is in Go.

## Results

### Settings on val

`eval/results/b2_eval_seed0_rv_val_*` (commit 9198c82, clean tree), `python -m eval.b2_compare grid --target room --noise-margin 1`. Mean val accuracy in points:

| Setting | Six reverberant conditions | Clean and five tempo | Two noise conditions | Qualifies |
|---|---|---|---|---|
| Baseline (D53) | 56.73 | 99.37 | 87.30 | |
| A, 0.4 s | 57.53 | 99.60 | 89.80 | yes |
| A, 0.8 s | 58.27 | 99.17 | 86.90 | yes, chosen |
| A, 1.5 s | 55.50 | 97.87 | 80.20 | no |
| B, 4 frames | 54.37 | 98.93 | 40.20 | no |
| B, 8 frames | 62.33 | 98.83 | 41.70 | no |
| B, 16 frames | 71.87 | 99.10 | 49.60 | no |

A barely moves the reverberant conditions. B lifts them by up to 15 points and halves the noise conditions, so every B setting fails the noise guard: crowd noise is full of onsets (speech, clatter), which B ranks highest.

### Test

`eval/results/b2_eval_seed0_rv_{base,a800,b16}.*` (commit f44aa08; 1,000 positives and 500 negatives per split, each calibrated on its own val; B with 16 frames is exploratory, outside the rule), `python -m eval.b2_compare pair`:

| Condition | Baseline | A, 0.8 s | B, 16 frames |
|---|---|---|---|
| Clean | 99.9 | 99.9 | 99.8 |
| Mean of clean and five tempo | 99.50 | 99.27 | 99.20 |
| Cafeteria, 0 dB | 86.9 | 83.1 (p 3e-6) | 44.6 (p 5e-124) |
| Train station, 0 dB | 86.4 | 83.9 (p 8e-4) | 53.6 (p 2e-90) |
| Room 0.4 s | 99.3 | 99.3 | 99.7 |
| Room 0.8 s | 74.6 | 76.3 (p 0.15) | 93.0 (p 2e-35) |
| Room 1.5 s | 27.4 | 31.0 (p 0.005) | 56.6 (p 3e-47) |
| Room 0.8 s and crowd | 33.4 | 33.9 (p 0.75) | 41.5 (p 2e-5) |
| Room 0.8 s and key lock +8% | 53.2 | 55.5 (p 0.08) | 64.8 (p 9e-10) |
| Room 0.8 s and turntable +8% | 38.9 | 42.2 (p 0.01) | 71.4 (p 2e-58) |

Test false accepts 0.16%, 0.10%, and 0.21% over all conditions.

- **A does not hold up.** It passed the val guard (0.4 points of noise lost), but on test it loses 3.2 points of noise accuracy, significantly, for 2 to 4 points in the rooms: not worth building.
- **B is a trade, not a fix.** It nearly doubles the triplets in reverberant rooms and halves them in crowd noise; built as is, it would undo D53.

## Recommendation

Build neither. Next, a variant that keeps what each does well, with its own rule fixed before its runs:

- **B gated:** peaks ranked by magnitude as today (with the region cap, D53), but a candidate must rise at least some threshold over its band's last frames to be a peak at all. A reverberant tail, decaying, never rises, so it gives up the slots it takes today; crowd onsets still have to outrank the music by magnitude, as today. Index and query, so a re-index if it wins.
- Later, if the gate is not enough: index both peak sets (magnitude and onset) and search both, at about twice the postings.

## Gated B: method (fixed before its runs, 2026-11-02)

`gate_frames` and `gate_db` in `prototype.b2.Params`: a candidate peak must rise `gate_db` over the most its band held in the preceding `gate_frames` frames; peaks are still ranked by magnitude, with D53's region cap. A candidate is already the largest within 8 frames either side, so a 0 dB gate over 8 frames or fewer changes nothing. The same 14 conditions, splits, and rule as A and B above (`grid --target room --noise-margin 1`), on val at 500 and 500, with the baseline re-run in the same batch: 16 and 32 frames with a 0 dB gate; 8 and 16 frames with a 3 dB gate; 8 and 16 frames with a 6 dB gate. Then val and test in full for the chosen setting, against the baseline's runs above.

### Gated B: results, and a design error

`eval/results/b2_eval_seed0_gt_val_*` (commit 9c4a122). Mean val accuracy in points:

| Setting | Six reverberant | Clean and five tempo | Two noise | Qualifies |
|---|---|---|---|---|
| Baseline | 56.73 | 99.37 | 87.30 | |
| 16 frames, 0 dB | 55.37 | 99.27 | 85.50 | no (noise) |
| 32 frames, 0 dB | 56.50 | 99.67 | 89.90 | yes, chosen, but below the baseline |
| 8 or 16 frames, 3 or 6 dB | 0 | 0 | 0 | no: no peaks at all |

The 3 and 6 dB gates passed no peak, so their indexes were empty. The cause is the triplets' front end: its 4,096-sample window spans 16 frames, so each frame overlaps the next by 94% and the spectrogram rises smoothly into a note; at a peak the frame before is nearly as loud. Over the 12,427 candidate peaks of 10 val tracks, the rise over the preceding 16 frames has a median of 0.18 dB and none reaches 3 dB; measured against the 16 frames before a delay of 8 frames, the median is 3.6 dB and 54.5% reach 3 dB (16 frames' delay: 3.1 dB, 50.8%). The gate as designed tests nothing, and B's ranking (above) mostly measured the last frame's slope too. The two 0 dB settings did nothing for reverberation.

## Gated B with a delay: method (fixed before its runs, 2026-11-02)

`gate_delay` frames are skipped before the `gate_frames` look-back: a candidate must rise `gate_db` over the most its band held from `gate_delay + gate_frames` to `gate_delay + 1` frames before it. Ranking unchanged (magnitude, D53's region cap). The same conditions, splits, and rule (`grid --target room --noise-margin 1`), the baseline from the batch above (`_gt_val_base`): a 16-frame look-back after a delay of 8 or 16 frames, with a 3 or 6 dB gate (4 settings). Then val and test in full for the chosen setting.

### Gated B with a delay: results

`eval/results/b2_eval_seed0_gd_val_*` (commit 5cda978). Mean val accuracy in points, the baseline from the batch before:

| Setting | Six reverberant | Clean and five tempo | Two noise | Triplet postings | Qualifies |
|---|---|---|---|---|---|
| Baseline | 56.73 | 99.37 | 87.30 | 22.0M | |
| Delay 8, 3 dB | 55.03 | 99.37 | 82.90 | 12.7M | no (noise) |
| Delay 8, 6 dB | 40.77 | 95.60 | 66.80 | 6.5M | no |
| Delay 16, 3 dB | 56.17 | 99.53 | 83.70 | 12.1M | no (noise) |
| Delay 16, 6 dB | 40.87 | 95.87 | 69.30 | 6.5M | no |

Gated B fails: the gates take away 45 to 70% of the peaks without helping the reverberant conditions, and cost the noisy ones. So the hypothesis behind it, that reverberant tails take the peak slots, is not what limits the triplets here, or a gate cannot tell tails from sustained notes. B's ranking (16 frames, no delay) is still the only change that lifted the rooms, by a mechanism not identified.

## Where this leaves reverberation

- Nothing tested is worth building: A does not hold up, B trades noise for reverberation, gated B does nothing.
- What remains untested: indexing both B's peaks and today's and searching both (each condition finds its matches in one set; about twice the postings and search cost); option C, longer windows; option D, a learned front end (milestone 3).
- Before more of it: whether the simulation is right. A phone recording of a few mixes played through speakers would say whether real rooms cost the triplets this much.

## A closer simulation, on real sets (2026-11-08)

`eval.phone_sim` replaces the statistical room for this check: PA speakers (band limits, a soft limiter), a shoebox room (direct sound and early reflections by the image-source method, a late tail that decays faster at high frequencies, its level from the venue's size and reverberation time), crowd noise at +8 to +10 dB, and a phone (high-pass, automatic gain, AAC at 128 kbps). Four venues: a patio (0.3 s), a small club (0.6 s), a mid-size club (0.9 s), a warehouse (1.6 s).

Run on two of Sean's own recorded sets (32 and 22 minutes, full-length songs, against the 791 songs of his USB library; the first test on real sets). There is no tracklist for either yet, so the reference is each set's own clean tracklist, and "strong" means an entry with evidence of at least 1,000 (`eval/results/local/phone_real_sets.json`, no song names):

| | Clean | Patio | Small club | Mid club | Warehouse |
|---|---|---|---|---|---|
| Set A: strong songs kept (of 17) | | 14 | 15 | 11 | 13 |
| Set A: share of the set covered | 81% | 41% | 32% | 27% | 28% |
| Set B: strong songs kept (of 11) | | 11 | 10 | 10 | 8 |
| Set B: share of the set covered | 75% | 35% | 30% | 26% | 18% |

Most songs are still named somewhere, but in short pieces: coverage falls by half or more even on the patio, so a phone recording would give a usable list of songs and a poor timeline. Each venue also adds 1 to 6 entries on songs the clean tracklist does not have, which may be wrong or may be real songs it missed.

