# Mixer moves: a learned model of what the DJ did on the mixer

Status: method fixed before any training run (2026-11-15); trained and tested 2026-11-16 (results at the end, D58). D54 R2 (a), Sean's pick; milestone 3 of the set reconstructor.

## The task

For every frame of a set (23 ms) and every song playing, estimate the DJ's gain on that song in each of the mixer's three EQ bands: low (below 250 Hz), mid (250 Hz to 2.5 kHz), high (above 2.5 kHz). The channel fader is the gain common to all three; a bass swap is the low band of one song falling while the other's rises; a filter sweep is the high or low band moving alone. These curves are the editable session's automation (Ableton volume and EQ lanes), the reconstructor's fades (D43), and the lighting work's knowledge of which song dominates which part of the spectrum (docs/design/light-show.md).

Inputs: the set's audio, and for each tracklist entry the song's own audio and its alignment (reference seconds = a + rate x set seconds, D55), as the reconstructor has them.

## Why learn it

The physics is known: in power, each frequency bin of the set is the sum of each playing song's power in that bin, times the square of that song's gain in the bin's mixer band. With two songs and dozens of bins per band, a per-frame, per-band non-negative least squares fit can solve it, and that is the baseline below. It breaks where the model does: misalignment of a few frames, effects (distortion, compression, filters that are not the three bands), noise, songs whose spectra in a band look alike, and frames where a song is nearly silent. A DJ's moves also have structure that a per-frame fit ignores: they are smooth, they follow phrases, a bass swap is two curves moving in opposite directions. A model trained on mixes whose moves are known can learn both the robustness and the structure. Whether it beats the fit is what this measures.

## Training data: rendered mixes with exact moves

`eval/mixsim.py` renders mixes at 11,025 Hz, mono, from fma_medium clips (30 s, Creative Commons; tracks in the val and test splits are never used for training). Each mix chains six songs; each transition overlaps 4 to 20 s. Each song plays at a tempo change drawn as in `eval.mixgen` (turntable or key lock, 0 with probability 0.2, else up to 8%), then goes through an isolator EQ (zero-phase crossovers at 250 Hz and 2.5 kHz, each a raised cosine over a third of an octave, so the bands sum back to the song exactly; a gain per band), then its channel fader, so the label is exact: the per-band gain at each sample. A master gain drawn per mix (-12 to +6 dB) is part of the label, so each gain is the song's level in the mix relative to its own file, which is what the audio determines. Each transition draws one move:

- **Fader crossfade**: linear, equal-power, S-curve, or a cut, over a random part of the overlap.
- **Bass swap**: the incoming song's low band held near silence until a swap point, then up while the outgoing song's goes down, faders already up.
- **Filter-style sweep**: the outgoing song's high or low band swept down over the overlap.
- **Mid dip**: the outgoing song's mid band lowered 6 to 15 dB while the incoming one's vocal enters.
- **Trim**: a static offset of up to 6 dB on any band, at any time.

After the mixer comes the recording chain, which the labels do not see, so a model cannot rely on the clean physics: a master limiter driven 0 to 6 dB into a soft clip, room noise 30 to 45 dB below the music, and an MP3 round trip at 128 kbps on half the mixes. (Added before any training run: on one clean mix with the true alignment, B1 already recovers the gains to 0.1 to 0.4 dB median error, so a clean synthetic test would say nothing about real sets.)

Labels are kept at the spectrogram's frames: each song's three gains (linear, and in dB floored at -40) at the frame's center.

## Models

- **B0** (D43, today): per frame, one broadband gain per song by non-negative least squares on the band powers, then the same smoothing as D43.
- **B1** (the non-ML baseline to beat): the same fit per mixer band, three gains per song per frame, then the same smoothing per band.
- **M** (the learned model): a temporal convolutional network over the frames. For each song it sees the set's and the song's warped power in 24 sub-bands (8 per mixer band, log), the other songs' summed warped power in the same sub-bands, and B1's three gains; it outputs the three gains in dB. Dilated 1-D convolutions (64 channels, six layers, dilations 1 to 32) give it a receptive field of about 6 s. Trained with an L1 loss in dB on frames where the song plays, on PyTorch (Apple GPU), with the alignment jittered by up to 2 frames and the rate by 0.1% so it does not depend on a perfect fit.
- **M + reconstruction** (only if M beats B1): fine-tuned on unlabeled real sets with a reconstruction loss (the log-spectral distance between the set and the sum of the warped songs under the predicted gains), to see whether real sets teach it anything synthetic ones do not.

## Evaluation (fixed now)

1. **Synthetic test** (150 mixes from the test split's tracks, seed 2, rendered once; 100 val mixes, seed 1, for model selection; 800 training mixes, seed 0; every song's alignment jittered by up to 2 frames and 0.1% of rate, seeded, in all three): mean absolute error of each band's gain in dB (floored at -40) over frames where the song plays; and the same restricted to transitions, where the moves are. Bass-swap detection: the frame of the swap point found within 1 s.
2. **UnmixDB test sets** (real club tracks, linear fader crossfades, no EQ labels): fade edges read from the broadband gain (the bands' gains weighted by the song's power in each band) with D43's ramp fit, against D43's published numbers (median fade-edge errors 0.77, 1.80, 1.53, 1.63 s; gain error in fades 0.28), on the same entries and alignments.
3. **Sean's two real sets** (no labels): the reconstruction error of the set from its verified tracklist (entries that pass the audio check, D57), B1 against M. Lower is better, and it says nothing about whether the moves are right, only that they explain the audio.

Model choices (width, depth, learning rate, training length) are made on the synthetic val split; the test numbers are computed once. M is adopted only if it beats B1 on the synthetic test and is not worse on UnmixDB's fades; otherwise B1 ships and M is written up as a negative result.

## Not in this milestone

Effects other than EQ and fader (echo, reverb, filters with resonance, loops, scratching); more than three songs at once; recognizing which mixer the DJ used.

## Results (2026-11-16, D58)

Data (`eval.mixsim`, then `eval.mixer_train features`): 800 training mixes (4,800 songs, 6.2M frames), 100 val (777k frames), 150 test (900 songs, 1.16M frames). M: 64 channels, six layers, batch 32, 8,000 steps on the Apple GPU (under two minutes each).

**Choices on val** (`eval/results/mixer/train_*.json`; mean absolute error in dB, low / mid / high): learning rate 1e-3 reached 1.56 / 1.23 / 1.27 at step 8,000, 3e-4 1.70 / 1.33 / 1.45; B1 3.69 / 2.55 / 3.78, B0 3.80 / 4.09 / 4.34. 1e-3 kept. In a look at val (not committed), B1's error concentrated where a band is killed: its fit cannot tell a band at -40 dB from one at -30.

**1. Synthetic test, once** (`eval/results/mixer/test_lr1e-3.json`):

| Mean absolute error, dB | Low | Mid | High | Low, in transitions | Mid, in transitions | High, in transitions | Bass swaps found within 1 s (of 222) | Median swap error |
|---|---|---|---|---|---|---|---|---|
| B0 (D43, broadband) | 4.30 | 4.50 | 4.71 | 6.09 | 6.04 | 6.22 | 75.2% | 0.23 s |
| B1 (per-band fit) | 3.93 | 2.86 | 3.88 | 5.51 | 3.78 | 5.43 | 85.6% | 0.07 s |
| M | **1.50** | **1.17** | **1.19** | **2.07** | **1.53** | **1.57** | **98.2%** | 0.07 s |

A swap point is read the same way from every method and from the labels: the frame where the incoming song's low band passes the outgoing one's (the split with the fewest frames on the wrong side). The swap count was added to the test command after its first run; the rerun reproduced the first run's errors exactly.

**2. UnmixDB's real fades** (`eval.dj_analysis --gains {b1,model:lr1e-3}`, the D53 rows and D55 alignment of the committed D43 run; `eval.fade_compare`, `eval/results/mixer/fades_lr1e-3.json`). Each method's per-band gains become one broadband gain per song (the bands weighted by the song's power in them), and the fades are read with D43's ramp fit. All three report the same 1,051 songs. Median error of each fade edge (s), and the gain curve's error inside fades:

| | Fade-in start | Fade-in end | Fade-out start | Fade-out end | Gain error in fades |
|---|---|---|---|---|---|
| B0 (D43, as committed) | **0.76** | **1.71** | **1.54** | 1.60 | **0.276** |
| B1 | 0.94 | 2.38 | 1.89 | 1.90 | 0.320 |
| M | 0.84 | 2.34 | 1.88 | **1.49** | 0.319 |

UnmixDB's transitions are fader crossfades with no EQ, the case one broadband gain fits exactly; splitting it into three bands fits fewer bins per gain, and the low band, which holds most of a club track's power and looks alike across club tracks, dominates the weighted sum. M reads them better than B1 on all four edges but not as well as D43 on three.

**3. Sean's sets** (`eval.mixer_sets`, `eval/results/mixer/sets_lr1e-3.json`; the D57 tracklists, 26 songs over 32.4 minutes and 12 over 21.7): reconstruction error (mean squared log10 power difference) B0 0.819 and 0.458, B1 0.594 and 0.348, M 0.588 and 0.355. Per-band gains explain the audio much better than broadband; M and B1 explain it about equally, though M's gains are 2 to 3 times closer to the truth on labelled mixes. So the reconstruction error barely sees what M improves; most of what is left is likely what no gain explains (effects, versions of a song that differ from the file, the room).

**The rule, applied.** M beats B1 on the synthetic test by a wide margin. "Not worse on UnmixDB's fades" was written against B1, the method it would replace, and M is not worse than B1 there; against D43's broadband fades, which the same sentence's evaluation quotes, both per-band methods are worse on three of four edges. So: M is adopted for what only a per-band method gives, the three EQ lanes per song (bass swaps, sweeps, dips) for the editable session; fades stay with D43's broadband fit, which reads fader moves best. **M + reconstruction** is not run: with two unlabelled sets, and a reconstruction error that cannot tell M's gains from B1's, fine-tuning on it would mostly teach the model to explain effects and version differences with gains. Better real-world evidence would come from sets with known EQ moves (a DJ's controller log, or Sean's own mixer recorded beside the audio).
