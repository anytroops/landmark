# Design: DJ-set tracklisting

Status: 2026-10-17. Sean decided piece 1 = A then B, piece 2 = ii + iii, and approved the Mixotic download (~10.4 GB) on condition that it is deleted (audio and its index) once the DJ-tracklisting work is finished and its results are committed.

## Goal

Given a recorded DJ mix (an hour or more of continuous audio), produce its tracklist: which catalog tracks were played, when each starts and ends, and how much the DJ sped it up or slowed it down. Abstain where the music is not in the catalog.

This turns Landmark from "identify one clip" into "explain a long, heavily modified recording", which needs (1) matching that survives tempo and pitch changes and (2) segmentation of a continuous stream into a sequence of tracks.

## Why the current system fails

DJs change tempo to beat-match, usually by a few percent, sometimes more, either turntable style (tempo and pitch move together) or with keylock (tempo moves, pitch stays). Landmark's hash is (f1, f2, dt) in absolute bins (D10), and votes need a constant time offset between query and reference (D11):

- Turntable +2%: every frequency moves up 2% (about 5 bins at bin 250) and every dt shrinks 2%, so almost every hash changes.
- Keylock +2%: frequencies stay, dt shrinks 2%, and the query-to-reference offset drifts by about 0.2 s over a 10 s query, smearing the votes across about 9 frames.

Measured on fma_medium (docs/EVAL.md): at +/-2% turntable speed, accuracy at threshold is 0.6% / 0.7% (raw top-1 61% / 63%), against 99.9% on clean queries.

## Evaluation data

| Set | What | Size | License | Use |
|---|---|---|---|---|
| **Mixotic** (Sonnleitner, Arzt, Widmer, ISMIR 2016) | 10 real DJ mixes (11 h 23 min), 723 reference tracks (118 played), hand-annotated song borders | ~1 GB mixes + ~9.4 GB references + 6 KB annotations | CC BY-NC-ND 3.0 | Main benchmark: real mixes, **published baselines** |
| UnmixDB (Schwarz, Fourer, 2018) | Synthetic 3-track mixes built from the Mixotic tracks, 4 effect x 3 time-scale variants, exact ground truth including speed factors | 4.2 GB | CC BY-NC-ND 4.0 | Optional: exact speed ground truth on real dance music |
| Synthetic FMA mixes (ours) | Mixes generated from indexed fma_medium tracks with crossfades, EQ, noise, and chosen speed changes (turntable and keylock, 0 to +/-8%) | generated | FMA's CC licenses | Controlled sweeps: accuracy vs speed, vs crossfade length |

Published results on Mixotic (Table 2 of the ISMIR 2016 paper): 20 s non-overlapping queries, scored per second of mix, no smoothing:

| Method | Accuracy | Precision | Specificity |
|---|---|---|---|
| Audfprint (Shazam-style pairs, like Landmark today) | 0.637 | 0.680 | 0.255 |
| Panako (triplets, ratio hashes) | 0.360 | 0.432 | 0.349 |
| Qfp (quads, range search + verification) | 0.876 | 0.959 | 0.927 |

We would report Landmark on exactly this protocol first (comparable to the table), then our full tracklister with smoothing as a separate, clearly labeled result.

**DECIDE (download):** Mixotic is ~10.4 GB, over the 5 GB ask-first limit; 78 GiB free. UnmixDB is another 4.2 GB and optional. Audio stays out of git as always.

## Piece 1: tempo- and pitch-robust matching

**DECIDE: pick one.**

| | A. Speed grid on today's hash | B. Ratio triplet hashes (Panako-style) | C. Quad descriptors with range search (Qfp-style) |
|---|---|---|---|
| Idea | Resample each query at a grid of speeds (e.g. -8% to +8%) and search each version with the current index | New fingerprint: hash groups of 3 peaks by ratios (time-gap ratio, log-frequency differences) that do not change under speed changes; estimate the speed from matched hashes and vote on (track, offset, speed) | Groups of 4 peaks normalized into a continuous 4D descriptor; candidates found by near-neighbor range search, then a verification step checks surrounding peaks |
| Turntable | Yes | Yes (with a log-frequency spectrogram) | Yes |
| Keylock | Only with a second grid axis (more cost) | Yes | Yes |
| What changes | Query side only; index, segment format, shard, coordinator unchanged | Fingerprint (Python reference + Go parity), matcher voting; segment format and search path unchanged (still u32 hash lookups) | Fingerprint, index structure (range search, not hash lookup), shard search path, verification stage: most of the core |
| Cost per query | x(grid size), about 17 for 1% steps over +/-8%; the stop list (D30) offsets part of it | About one search, more postings per hash than today (to measure) | Range queries are costlier than lookups; needs the most new code |
| Risk | False accepts grow with more attempts per query (recalibrate thresholds) | Ratio hashes are less specific; Panako scored low on Mixotic (0.36), so the design needs care | Highest effort; best published accuracy |
| Systems story | Uses the cluster's capacity: N searches per query, fan-out at scale | Same infrastructure, smarter algorithm | New index and search structure |

**Decided (Sean, 2026-10-17): A first, then B.** A is days of work, needs no index change, and gives an honest baseline plus the tolerance curve. B is the real contribution: speed-proof matching in the same distributed index, and the result to compare against the published table. C is the strongest published method but replaces most of the core; it is the fallback if B cannot get close to Qfp. First step either way: measure today's hash's speed tolerance (accuracy vs speed error in 0.25% steps), which sizes A's grid and quantifies the problem.

## Piece 2: from window matches to a tracklist

**DECIDE: pick one.**

| | i. Smooth window results | ii. Sequence decoding (Viterbi) | iii. Per-track alignment |
|---|---|---|---|
| Idea | Identify overlapping windows (e.g. 10 s every 2.5 s), then majority-vote over neighbors | Treat the mix as a sequence of states (one per track, plus "unknown"); window scores give the evidence, switching tracks costs a penalty; Viterbi finds the best tracklist | For each candidate track, fit a line through its matched (mix time, reference time) pairs; the slope is the speed and the line's extent gives start and end |
| Handles crossfades | Poorly (flickers) | Well (one switch, placed where evidence changes) | Well (overlapping spans are allowed) |
| Uses time consistency | No | Through the penalty only | Yes: a played track's reference time must advance linearly with mix time |
| Effort | Small | Medium | Medium |

**Decided (Sean, 2026-10-17): ii + iii.** Alignment (iii) supplies trustworthy per-track evidence (a real play advances linearly at a constant speed; chance matches do not), and Viterbi (ii) turns it into one clean, non-flickering tracklist with an explicit "unknown" state. Smoothing (i) is the baseline it must beat.

## Metrics

- Published protocol on Mixotic: per-second accuracy, precision, specificity with 20 s non-overlapping queries, no smoothing (comparable to the table above).
- Tracklist quality: track-level precision and recall, boundary error (seconds) at tolerances of 5, 10, 20 s, speed estimate error.
- Cost: CPU seconds per hour of mix, and queries per second the cluster sustains with the chosen method (B2-style).
- Regression: clean and noisy single-clip accuracy on fma_medium must not drop (EVAL.md conditions), FAR <= 0.1%.

## Plan

1. Download Mixotic (after approval); index its 723 references with `cmd/ingest`, alone and together with fma_medium as distractors.
2. Measure today's speed tolerance and Landmark's score on the Mixotic protocol (the "before").
3. Piece 1 per the decision, with Python reference and Go parity as before; eval on synthetic FMA mixes and Mixotic.
4. Piece 2 per the decision; tracklist metrics.
5. Write-up: before/after on Mixotic against the published table; cost numbers.

## Limitations to state

- fma_medium tracks are 30 s clips, so synthetic FMA mixes switch tracks every 20 to 30 s; Mixotic has full-length tracks and real transitions.
- Mixotic's song borders are approximate (long fades), per its authors.
- Licenses are non-commercial, no-derivatives: fine for evaluation, never redistributed.

## Sources

- Sonnleitner, Arzt, Widmer. Landmark-based Audio Fingerprinting for DJ Mix Monitoring. ISMIR 2016. Dataset: http://www.cp.jku.at/datasets/fingerprinting/
- Six, Leman. Panako: a scalable acoustic fingerprinting system handling time-scale and pitch modification. ISMIR 2014.
- Sonnleitner, Widmer. Quad-based audio fingerprinting robust to time and frequency scaling. DAFx 2014; Robust quad-based audio fingerprinting. IEEE/ACM TASLP 24(3), 2016.
- Schwarz, Fourer. UnmixDB: a dataset for DJ-mix information retrieval. ISMIR 2018 late-breaking. https://zenodo.org/record/1422385
- Kim et al. A computational analysis of real-world DJ mixes using mix-to-track subsequence alignment. ISMIR 2020.
