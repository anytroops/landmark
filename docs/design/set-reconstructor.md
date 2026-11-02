# Set reconstructor: a recorded DJ set back as an editable session

Status: decided (2026-11-01, D54): Sean took the recommendation on all four decisions: R1 (b) after a local core, R2 (a), R3 as proposed, R4 (b). Nothing built yet; milestone 1 next.

## Why this, and for whom

Sean's goals for the project, in order: fill what his CS coursework leaves out (now machine learning, and cloud and deployment; systems, distributed services, performance, and DSP are covered), be complete and demoable, and keep a credible path to a company. He DJs on Rekordbox and produces in Ableton Live, FL Studio, and Logic Pro, so he is the first user. No deadline: quality over speed. Built nights and weekends.

The product: drop in a recording of a set and get back what was played and how it was mixed, as a session you can edit and open in Rekordbox or a DAW.

**The catalog is your own library, not a commercial one.** Rekordbox exports its collection as XML (each track's file path, BPM grid, and cue points). The reconstructor fingerprints those files, so a set is matched against the songs the DJ owns. That is where Landmark is strongest (the right songs are in the index, at changed tempo, inside a mix), and it needs no licensing: nothing is distributed, and exports point at the user's own files.

**The business path stays open.** The same engine, given a commercial catalog, is what rights work needs (who played what, for payouts): the market Dubset, DJ Monitor, and Pex serve. Consumer tool first, as the portfolio piece and the way to prove accuracy; the API for platforms, rights holders, and venues later, with Sean's contacts (labels, clubs and festivals, platforms, DJs) as early testers.

## What exists and what is new

Exists: tracklisting that survives turntable and key lock changes (D36, D37, D53: 95% of UnmixDB test songs at 98.6% precision), each entry's tempo factor and offset in the source, gain curves and turntable-or-key-lock calls (D43), adding songs to an index while it serves (D42, D47), live monitoring, and a demo page with a timeline (D46).

New:

1. **Library import.** Read a Rekordbox XML export; fingerprint the tracks into the user's index; keep each track's beat grid and cues.
2. **The session.** Per song: source file, in and out points in the source, place in the set, tempo factor, turntable or key lock, gain curve, and (with the ML part) EQ moves.
3. **An editor.** A timeline to check and correct the session: fix a wrong song, move a boundary, and play the reconstruction against the recording to hear the difference.
4. **Exports.** Rekordbox first (a playlist in set order, with memory cues at the points each song came in and went out), then an Ableton Live set (each song a warped clip at its tempo, with volume and EQ automation), then a format any DAW opens (one rendered audio file per song, aligned to the set, plus a tempo map) for FL Studio and Logic.
5. **A hosted service** (the cloud gap) and **a learned model** (the ML gap): options below.

## Decisions for Sean

### R1. Where it runs

- **(a) Local first, cloud later.** A local server and web page, as the demo is today. Fastest to a working tool; the cloud gap waits.
- **(b) Cloud, with a local agent.** A small agent on the DJ's machine fingerprints the library and uploads only fingerprints (no audio leaves the machine for the index); sets are uploaded for analysis. The service: accounts, storage, a job queue, per-user indexes, deployment, CI/CD, monitoring. Fills the cloud gap with a real reason for each piece, and is the shape the later API needs.
- **(c) Cloud only.** Upload library and sets. Simplest service, but uploading a whole library is slow and asks users to hand over their music.

Recommendation: (b), built after a local version of the core (so the first milestones are usable on Sean's machine). Open questions it raises: gain curves need reference audio at analysis time (D43), so either the agent computes them locally or uploads short reference excerpts; and per-user indexes cost memory (the shared catalog's triplet index is about 1 GB for 24,287 tracks), which makes deduplicating tracks many DJs own a real systems problem.

### R2. The ML component

- **(a) Learned mixer moves.** Given the recording and the identified songs aligned in time, estimate what the DJ did on the mixer: each song's gain and 3-band EQ over time. Supervised, with unlimited exact training data from `eval.mixgen` (which already renders mixes with EQ and bass swaps and knows the truth), a baseline to beat (D43's least-squares gain fit), and UnmixDB for a real-world check. Feeds the session directly (EQ automation in the exports). Small enough to train on this laptop.
- **(b) A learned fingerprint.** Train an audio embedding with contrastive learning to be robust to noise and tempo change (as neural audio fingerprinting does), and measure it against Landmark's hand-built fingerprints on the same eval harness. Strong research story and relevant to the business side (songs not in the library: "closest match" suggestions). Needs far more training compute, and may honestly lose to what exists.
- **(c) Stems.** Run a pretrained source separation model (Demucs, MIT licensed) to split the recording into parts. Useful for the editor, but using a pretrained model shows little ML skill.

Recommendation: (a) as part of the reconstructor; (b) later as a research track if Sean wants it.

### R3. Export order

Recommendation: Rekordbox XML first (Sean's DJ software; a documented import format), Ableton Live second (its project file is gzipped XML, undocumented but widely written by other tools; versions are a risk), and rendered per-song audio with a tempo map third (FL Studio and Logic have no practical project format to write).

### R4. The editor's front end

- **(a) Extend the demo page** (plain HTML, CSS, and JS, no build step, D46).
- **(b) A new app in React or Svelte with TypeScript.** More code to set up, but the timeline editor is the most complex UI so far, and a typed component framework is what a reader of the resume expects to see.

Recommendation: (b) for the editor; the demo page stays as it is.

## Milestones

1. **Reconstruct from a Rekordbox library, locally.** Import the XML, index the library, reconstruct a set, export a Rekordbox playlist with cues. Checked on Sean's own recorded sets (he knows what he played) and on UnmixDB.
2. **The editor.** Timeline, corrections, playback of the reconstruction against the recording.
3. **The ML model** (R2), trained, compared with the baseline, written up like the other evals.
4. **The hosted service** (R1), deployed with accounts, the local agent, and monitoring.
5. **Ableton export, then rendered audio for FL Studio and Logic.**
6. **The business check.** Sean's contacts try it; a short write-up of what platforms, labels, or venues would pay for.

Each milestone ends usable and written up, so the project reads as complete wherever it stops.

## Not decided here

Pricing, a name, and whether to incorporate; booth recordings (from the mixer's record output, clean) are the first target, phone recordings in a club (noisy; D52, D53) later.
