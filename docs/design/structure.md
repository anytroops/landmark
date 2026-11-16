# Song structure: phrases and drops from the audio, for songs Rekordbox did not label

Status: method fixed before any training run (2026-11-16); trained and tested the same day (results at the end). R1 adopted. The lighting work's second ML piece (docs/design/light-show.md).

## Why

A light show that hits the drop on the drop needs each song's structure: where the intros, builds, drops, breakdowns, and outros are. Rekordbox's phrase analysis gives it, but only for the songs it was asked to analyse: 361 of the 1,171 in Sean's USB export, and none of the 28 songs of his set A set. Beat grids are there for all of them (prototype.anlz), and once corrected against each song's own audio they land on the recorded set's beats within a median 10 ms (eval.beat_align). So the missing piece is the phrases, from the audio, on the grid.

## Labels and data

The 263 phrase-labelled songs of the export whose audio is on disk (212 in Rekordbox's high-energy mood, 39 mid, 12 low), split by song with a fixed seed: 183 train, 40 val, 40 test. Labels per bar of the beat grid (4 beats): the phrase kind Rekordbox gives, mapped to one set across moods: intro, verse, build (Rekordbox's "up"), drop (its "chorus" in the high mood; chorus elsewhere), breakdown ("down"), bridge, outro; and whether a phrase starts on that bar. A drop event is a drop or chorus phrase that follows a build, verse, or bridge.

These are Rekordbox's analysis, not a human's: the model learns to reproduce it, and is measured against it.

## Models

- **R0** (the non-ML baseline): phrase boundaries where a self-similarity novelty curve over bars peaks (Foote's checkerboard kernel over 8 bars, on the features below), restricted to multiples of 8 bars from the first downbeat; each phrase labelled by its energy against the song's median (low energy at the start: intro; at the end: outro; rising into a high one: build then drop; high: drop; low in the middle: breakdown).
- **R1** (the learned model): per bar, features pooled from the triplet front end's spectrogram over the bar's four beats (24 sub-band log powers, their change since the previous bar, the onset strength, the low-band share), into a bidirectional GRU (two layers, 64 units) over the song's bars, with two heads: the phrase kind (7 classes) and a phrase start (binary). Trained with cross-entropy on PyTorch, choices (width, learning rate, epochs) on the val split.

## Evaluation (fixed now)

On the 40 test songs, computed once:

1. Phrase starts: precision, recall, and F1 against Rekordbox's, a start counting as found within one bar.
2. Phrase kind per bar: accuracy, and a confusion table.
3. Drops: precision and recall of drop events within one bar, the cue the light show leans on most.
4. On Sean's set A (no labels): the share of the set's time under a predicted phrase, and how many drops the show gets, as a sanity check only.

R1 replaces Rekordbox's phrases only where Rekordbox has none; it is adopted only if it beats R0 on drop recall and phrase-start F1.

## Results (2026-11-16)

Data (`python -m eval.structure_train data --usb ~/Desktop`): 263 songs, 29,401 bars, 183 / 40 / 40 by song; per bar 47.4% drop, 21.5% build, 9.3% outro, 7.5% breakdown, 7.3% intro, 6.1% verse, 0.9% bridge; a phrase starts on 11.8% of bars; 602 drop events.

Choices on val (`eval/results/structure/train_*.json`; the score is the mean of kind accuracy and phrase-start F1, best epoch kept): width 64 or 128, learning rate 1e-3 or 3e-4, 40 epochs. All four within 0.01 of each other (0.723 to 0.733); width 128 at 1e-3, epoch 14, kept. R0 on val: kind 59.3%, start F1 0.48, drop recall 0.33.

Test, once (`eval/results/structure/test_h128_lr1e-3.json`; 40 songs, 4,326 bars, 91 drops):

| | Phrase-start F1 (precision, recall) | Kind per bar | Drop precision | Drop recall |
|---|---|---|---|---|
| R0 | 0.50 (0.69, 0.39) | 49.8% | 0.36 | 0.22 |
| R1 | 0.72 (0.69, 0.75) | 71.5% | 0.48 | 0.54 |

R1 beats R0 on drop recall and phrase-start F1, so it is adopted for songs Rekordbox did not label. It finds 90% of drop bars; its errors are verses called drops (174 of 298 verse bars: in Rekordbox's mid and low moods a chorus counts as a drop here, so verse and drop are close neighbours) and builds called drops or intros (241 and 101 of 1,043). Drop events are weaker on test than on val (0.48 and 0.54 against 0.65 and 0.79), from 40 songs; a drop placed more than a bar off counts as missed.

On Sean's two sets, where Rekordbox has phrases for none of the 38 songs, the shows now get phrases for all of them: 25 drops in set A's 26 songs, 18 in set B's 12 (`eval.lightshow --structure h128_lr1e-3`). Whether they land on the real drops is for Sean to judge by watching.
