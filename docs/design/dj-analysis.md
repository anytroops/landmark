# Design: DJ mix analysis

Status: draft, 2026-10-21. Decisions marked **DECIDE** need Sean's pick. Roadmap item 4.

## Goal

Go past "which songs, when" to "how it was mixed": for each transition, when the next song comes in and the last one leaves (the overlap), how the DJ crossfaded (the gain of each song over time), and the tempo of the set over time. This is what DJ-mix reverse engineering research studies (UnmixDB was built for it), and identification services do not report it.

## What exists

- Per window (10 s, hop 2.5 s): each search's top candidate with its score, the B2 line fit's offset and tempo factor, and the runner-up score. The other 19 candidates are returned and dropped.
- Tracklist entries with start, end (window resolution), and a median tempo factor.
- UnmixDB ground truth per song: where it starts in the mix, its fade-in and fade-out intervals, and its tempo change. Its README says the mixes use beat-aligned linear crossfades, so each song's true gain curve follows from the labels.

## A1. Transitions and overlaps

**DECIDE: pick one.**

| | (a) From window answers | (b) Fine re-search near transitions | (c) Gain curves from the reference audio |
|---|---|---|---|
| Idea | Keep the top 2 tracks per window with their fits; an overlap is the windows where both have on-line evidence | Around each detected change, search 2 s windows every 0.5 s restricted to the two tracks, and find where each one's score rises and falls | Both songs and their alignments are known (offset and tempo from the fits), so take each reference's spectrogram, warp it onto the mix's timeline, and solve for each song's gain over time (least squares, gains non-negative) |
| Output | Overlap start and end at 2.5 s resolution | Start and end to about 0.5 s | A gain curve per song (fade shape, start, end, and where the DJ cut the bass, if the effect is a bass swap) |
| Needs | Nothing new | A search restricted to given tracks (new shard request field) | The reference audio at analysis time (a reference store; the index holds only hashes) |
| Comparable with published work | No | No | Possibly: the dataset's authors published reverse-engineering results on it (metrics to check) |
| Effort | Small | Medium | Medium to large |

Recommendation: (c), with (a) as the baseline it must beat. It is the only option whose output is the actual mix (fade shapes, not just times) and whose error can be put next to published results. Needing the reference audio is a real cost and is stated: a rights holder or a DJ-set service has the catalog audio; a phone app does not.

## A2. Tempo curve

**DECIDE: pick one.**

| | (a) Relative tempo | (b) Relative tempo plus BPM |
|---|---|---|
| Idea | Per window, the B2 fit's tempo factor for the playing song, smoothed per song (a turntable change is constant within a song; a gradual tempo ride shows as a slope) | Also run a beat tracker on each reference at ingest and store its BPM; the set's BPM over time is the reference BPM times the factor |
| Output | "+2.1% from the original" over time | "126 BPM rising to 128" over time |
| Checked against | UnmixDB tempo changes | the same, plus the beat tracker's own error on the references |
| Effort | Small | Medium (beat tracking at ingest, a catalog field) |

Recommendation: (b): BPM is how DJs talk about a set; the relative factor alone is an engineer's number.

## A3. Where it appears

**DECIDE: pick one.**

| | (a) `cmd/tracklist -analyze` | (b) Also live events |
|---|---|---|
| Idea | The offline command adds transitions, overlaps, gain curves, and the tempo curve to its JSON output (and a plot script) | Live monitoring also logs a transition event once both songs' entries are known |
| Effort | Medium | Medium plus (a) |

Recommendation: (a) first; (b) once the analysis is measured.

## Metrics

On the UnmixDB test sets: transition start and end error (seconds), overlap duration error, gain-curve error against the linear ramps the labels define, tempo error per song and over time, and BPM error against the beat tracker run on the unmodified references.

## Limitations to state

- Effects other than gain (EQ, filters, reverb) are outside the model; UnmixDB's "bass" and "distortion" variants will show it.
- Gain curves need the reference audio, so this is a catalog-owner feature.
