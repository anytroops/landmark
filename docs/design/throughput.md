# Design: more live streams per machine

Status: draft, 2026-10-22. Decisions marked **DECIDE** need Sean's pick.

## Where the time goes

One laptop keeps up with 32 live stations, not 48 (BENCHMARKS B9). Per 10 s window of one stream, from committed measurements:

- Fingerprinting, 487 ms of CPU (BENCHMARKS B12, one core): the triplet query fingerprint of the window (124 ms), of the window resampled to double time (20 s of audio, 261 ms) and to half time (63 ms), and the pair fingerprint (31 ms).
- Searches, about 145 ms of CPU: 3 triplet searches at about 32 ms each and 3 pair searches at about 16 ms (12 cores divided by B8's 378 and 734 qps).

Fingerprinting is about three quarters of the work, and windows overlap by 75% (10 s every 2.5 s), so every second of audio is fingerprinted four times, each time on a grid that starts somewhere else: 2.5 s is 27,562.5 samples, not a whole number of 256-sample frames, so no two windows share a spectrogram frame.

## T1. Cutting the fingerprinting

**DECIDE: pick one.**

| | (a) Shared front end, frame-aligned hop | (b) Fewer hypotheses live | (c) Fewer sub-hop shifts live | (d) Change nothing; add machines |
|---|---|---|---|---|
| Idea | Make the hop a whole number of frames (2.5077 s = 108 frames of 256 samples), compute each stream's spectrograms once as audio arrives, and cut each window's spectrogram out of them; peak picking and hashing stay per window | Drop the double- and half-time searches in live monitoring (the "two-search hybrid") | Fingerprint each window on 2 sub-hop grids instead of 4 (D15) | Leases already spread streams over workers; run workers on more machines |
| Fingerprinting saved | Up to about 3/4 of the spectrogram work (each frame computed once instead of four times; peak picking and hashing stay per window) | About 2/3 (the resampled windows: 323 of 487 ms) | About half | None |
| Accuracy | Unchanged once recalibrated for the new hop (to check); live still equals offline when both use the new hop | Measured: 93.7% of UnmixDB songs against 95.2% (D37) | To measure (D15 measured the gain of 4 shifts on clips) | Unchanged |
| Effort | Medium: a streaming spectrogram in Go, the hop change through tracklist, monitor, and eval, a recalibration | Small (a flag) | Small | None in code; needs machines |

Recommendation: (a). It removes the duplicated work without giving up any of the hybrid's searches, and the exact live-equals-offline property survives because the offline tracklister would use the same hop. (d) stays the answer for scale beyond one machine and is what leases were built for.

## T2. Merging added songs

**DECIDE: pick one.** (Adding songs, D42: today `ingest -merge` rewrites the partition's whole base, 108M postings in 36 s, B10; the cost grows with the catalog, not with what was added.)

| | (a) Size-tiered merges | (b) Leveled merges | (c) Keep merging everything into the base |
|---|---|---|---|
| Idea | Merge deltas with each other once several of similar size pile up; fold them into the base only when they reach a set fraction of it (e.g. 10%) | Keep a few levels, each about 10x the one before; a full level merges into the next | As today |
| Merge cost | Mostly proportional to what was added; an occasional full rewrite | Steady, about proportional to what was added, more total rewriting | Proportional to the catalog every time |
| Searches | A few more segments per search between merges | Few segments | One segment after each merge |
| Effort | Small to medium | Medium | None |

Recommendation: (a), the LSM idea in its simplest form; the search cost of a few extra segments is measurable with the existing load harness.
