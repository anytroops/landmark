# Reverberation: options, measured before building

Status: problem measured (2026-11-02); options for Sean, not yet tested.

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

## Recommendation

Test A and B on the triplets first, where the loss is, in the prototype (`eval.b2_eval` builds its index in memory, so B costs a run, not a rebuild): A first, since it needs no new index if it wins. C and D later if neither is enough. Building a winner is a core change and waits on Sean's pick, as D53 did.
