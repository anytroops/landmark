# Design: head-to-head against open-source fingerprinters

Status: decided 2026-10-21 (D41: H1 (c), H2 (a), H3 (b)); running. Roadmap item 2 (after live monitoring).

## Goal

Put Landmark's numbers next to systems other people built and published, on the same audio, queries, and machine, so "99.9% clean, 97% at 0 dB, 95% of DJ-mix songs" means something to a reader. Today every comparison in EVAL is Landmark against itself.

## Candidates

- **audfprint** (Dan Ellis, Python): landmark pairs with an offset histogram, the same family as Landmark's pair fingerprint (Wang 2003). The natural baseline for clean, noisy, and MP3 queries. Not tempo robust.
- **Panako** (Joren Six, Java): triplet hashes from ratios that survive tempo and pitch changes (Six and Leman 2014; Panako 2.x), the design B2 follows. The natural baseline for turntable and keylock changes and for DJ mixes.
- Out of scope: Dejavu and Olaf (kept to one tool per fingerprint family: pairs and tempo-robust triplets), commercial systems (no access).

Installing either is a download: audfprint is a pip install from GitHub (small); Panako needs a Java JDK (a few hundred MB) and a Gradle build. Both need Sean's approval first.

## H1. What to compare

**Decided (see the status line).**

| | (a) Clip identification only | (b) Clips and DJ mixes | (c) Clips, DJ mixes, and cost |
|---|---|---|---|
| Clips | fma_medium test queries (10 s): clean, noise 0 to 10 dB, MP3, turntable and keylock +/-1 to 8% | same | same |
| DJ mixes | none | UnmixDB test sets: each system's per-window answers through the same decoder, plus Panako's own over-time matching | same |
| Cost | none | none | index build time and size, query time per clip on one core and under load |
| Effort | Small | Medium (window loop for each tool) | Medium to large |

Recommendation: (c). Accuracy without cost invites the obvious objection (a slower system can be more accurate), and the DJ mixes are where Landmark should differ most from audfprint.

## H2. Catalog

**Decided (see the status line).**

| | (a) Full fma_medium (25k tracks) | (b) A 3,000-track subset |
|---|---|---|
| Realism | The catalog every other EVAL number uses | Smaller than any real deployment |
| Risk | Panako's index build and audfprint's hash table may be slow or need tuning at 25k | Low |
| Comparable with EVAL | Directly | Only within this comparison (B2 vs pairs used 3,000) |

Recommendation: (a), with (b) as the fallback if a tool cannot index 25k tracks in reasonable time (that itself is a result to report).

## H3. Settings

**Decided (see the status line).**

| | (a) Defaults | (b) Defaults plus one tuned setting each |
|---|---|---|
| Idea | Each tool as shipped | Also the documented knob for each tool's density (audfprint `--density`, Panako's peak and hash settings), chosen on the validation queries |
| Fairness | Landmark was tuned on this data; others were not | Gives each tool one tuning pass on the same validation split |

Recommendation: (b). Landmark's thresholds were calibrated on the validation split (D13); the others get the same chance, chosen on validation and reported on test.

## Method

- Same audio decode (ffmpeg to each tool's expected rate), same query excerpts and degradations (`eval.degrade`), same machine, results through the existing Wilson-CI scoring and threshold calibration.
- Each tool's accept rule calibrated on validation to the same false-accept rate as Landmark's (D13), so recall is compared at matched precision.
- Results in EVAL ("Against open-source systems"), costs in BENCHMARKS, and the tools removed afterwards (Sean's cleanup rule).

## Limitations to state

- One machine; each tool's own parallelism.
- Tools are run as their authors ship them; a tool's weakness may be fixable with more tuning than one pass.
