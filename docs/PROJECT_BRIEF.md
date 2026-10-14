# Landmark: Project Brief and Export Log

Working name: **Landmark** (from landmark hashing; rename freely).
Created: 2026-10-14. Owner: Sean Xue.
Place this file at `docs/PROJECT_BRIEF.md` in the repo. `CLAUDE.md` goes at the repo root.

---

## 1. Mission

Build a distributed audio fingerprinting and identification service (Shazam / YouTube Content ID style) in Go. Audio is the domain; distributed systems is the work.

Why it exists: Sean's resume already performs well at banks and finance firms but underperforms at FAANG SWE resume screens. The gap is concrete evidence of backend and distributed-systems engineering: services under concurrent load, data bigger than one machine, failure handling, and latency numbers. This project produces that evidence, measured and reproducible, and gives Sean a system he can whiteboard and defend in depth.

Target payoff windows: late or spring 2027 intern postings, and new-grad recruiting opening summer 2027.

### Definition of success
1. A partitioned, replicated identification service indexing all ~106k FMA tracks.
2. Reproducible benchmarks: latency under load, partition scaling, failure behavior, index compression.
3. Rigorous accuracy evaluation with confidence intervals.
4. A public demo that works from a phone in a noisy room.
5. Sean can explain every design decision without notes.

### Non-goals (v1)
- Beating Shazam on accuracy.
- Neural/embedding fingerprints.
- Kubernetes for its own sake, LLM features, a polished consumer UI.

---

## 2. Context about Sean (for the agent)

- CMU student, Statistics & ML + Music Technology, AI minor, graduating May 2028.
- Strong: Python (FastAPI, numpy, pandas, librosa), C/C++ (embedded DSP on STM32/Daisy Seed, optimized the LEAF DSP library), React/TypeScript, SQL. Shipped production full-stack tooling as a SWE intern (summer 2026).
- Deep DSP background: STFT, windowing, spectral analysis. Do not over-explain DSP.
- New to: Go, gRPC, distributed systems in practice. Has not taken 15-213 (Computer Systems) or 15-210, so explain OS and systems concepts (mmap, page cache, syscalls, memory layout, concurrency primitives, tail latency) concretely the first time they come up.
- Time budget: roughly 10 hours/week alongside courses and recruiting. Sessions are often short. Leave the repo green at the end of every session.
- Recruiting OAs and interviews take priority over this project.
- Communication: direct, blunt, specific. No filler, no hype, no generic advice.

---

## 3. Hard rules

1. **No AI attribution.** Never add "Co-Authored-By: Claude", "Generated with Claude Code", or any AI attribution to commits, PR titles or descriptions, code comments, or docs. (Settings in section 18 enforce this; this rule is the backstop.)
2. **No em dashes** anywhere: code comments, docs, commit messages, chat. Use commas, colons, periods, or parentheses.
3. **No fabricated numbers.** Every performance or accuracy figure in README, docs, or resume bullets comes from a committed run with raw output in `bench/results/` or `eval/results/`. Unmeasured values are written as `[TBD]`.
4. **Explain-then-build** on core components (section 4).
5. **Measure before optimizing.** Use pprof evidence before claiming a bottleneck.
6. **Keep main green.** `go vet`, `go test -race`, lint, and Python tests pass before every commit.
7. **Ask before big downloads.** Anything over ~5 GB (including fma_small, fma_medium, fma_large) needs Sean's confirmation of disk space first. Never download fma_full.
8. **Never commit** audio, datasets, decoded PCM, or built index segments.
9. **Context degradation.** If context gets significantly degraded or compacted, say so explicitly and write a handoff to `docs/handoffs/YYYY-MM-DD.md` (state, next steps, open questions, gotchas) before continuing.

---

## 4. Working mode

The project's interview value depends on Sean owning the design.

- **Claude writes freely:** scaffolding, build files, Makefile, CI, Dockerfiles, compose, proto boilerplate, test harnesses, benchmark and eval scripts, plotting.
- **Core components** (fingerprinter, matcher, segment format, shard search path, coordinator fan-out and failover, replication): Claude proposes the design with 2-3 options and tradeoffs, Sean picks, then Claude implements in small reviewable diffs with a short explanation of each non-obvious piece.
- **After each core component:** Claude asks Sean 3 interview-style questions about it. Log questions and Sean's answers in `docs/INTERVIEW_NOTES.md`.
- **Every non-trivial decision** gets a `docs/DECISIONS.md` entry: context, options considered, decision, consequences, date.
- If Sean says "just write it," write it, but still log the decision and still ask the questions afterward.

---

## 5. Background reading

- Avery Wang, "An Industrial-Strength Audio Search Algorithm," ISMIR 2003. The landmark (constellation) algorithm this project starts from.
- Jeffrey Dean and Luiz André Barroso, "The Tail at Scale," Communications of the ACM, 2013. Why fan-out amplifies tail latency; hedged requests.
- Gil Tene on coordinated omission, for load-testing correctness.
- Stretch (tempo/pitch robustness): Joren Six's Panako; Sonnleitner and Widmer's quad-based audio fingerprinting.

---

## 6. Data

**FMA: A Dataset for Music Analysis** (github.com/mdeff/fma). Audio is Creative Commons, under each artist's chosen license; the dataset is meant for research use.

| File | Contents | Size | SHA1 |
|---|---|---|---|
| `fma_metadata.zip` | metadata for all 106,574 tracks | 342 MiB | `f0df49ffe5f2a6008d7dc83c6915b31835dfe733` |
| `fma_small.zip` | 8,000 tracks, 30s clips | 7.2 GiB | `ade154f733639d52e35e32f5593efe5be76c6d70` |
| `fma_medium.zip` | 25,000 tracks, 30s clips | 22 GiB | `c67b69ea232021025fca9231fc1c7c1a063ab50b` |
| `fma_large.zip` | 106,574 tracks, 30s clips | 93 GiB | `497109f4dd721066b5ce5e5f250ec604dc78939e` |
| `fma_full.zip` | untrimmed tracks | 879 GiB | do not use |

Download base URL: `https://os.unil.cloud.switch.ch/fma/` + filename. Verify with `sha1sum -c` (or `shasum -a 1` on macOS). If unzip fails, try 7zip (known compression issue in the FMA repo).

Plan: develop on fma_small, scale to fma_medium, final benchmarks and eval on fma_large.

Data handling:
- Some FMA MP3s are corrupt or truncated (see the FMA wiki errata). Ingest must log, skip, and count decode failures, never crash.
- **Do not cache decoded PCM** for the full set. Mono 11,025 Hz float32 is ~1.3 MB per 30s clip, so ~140 GB for fma_large. Decode on the fly through an ffmpeg pipe.
- fma_large disk plan: ~93 GiB zip plus about the same extracted. Delete the zip after verify and extract.
- Noise for evaluation: MUSAN noise subset (OpenSLR resource 17), or noise Sean records himself (crowd, club, street). Check license before committing any samples; prefer downloading at eval time.
- Licensing: store fingerprints and metadata only. The demo never serves audio. For the DJ-mix stretch, Sean's own library is fingerprinted locally; that audio never enters the repo.

---

## 7. Algorithm spec (v1 baseline)

All parameters are tunable. Record every change and its measured effect in `DECISIONS.md`.

**Decode:** ffmpeg to mono, 11,025 Hz, float32:
`ffmpeg -v error -i in.mp3 -ac 1 -ar 11025 -f f32le -`

**STFT:** Hann window, n_fft = 1024 (~93 ms), hop = 256 (~23.2 ms per frame), log magnitude. Frequency resolution ~10.8 Hz per bin; use bins 0-511.

**Peak picking:** 2D local maxima via a max filter over a neighborhood (start around 15-20 bins by 15-20 frames), plus an amplitude floor. Target ~20-40 peaks per second of audio; tune for robustness vs index size.

**Landmarks:** for each anchor peak (t1, f1), pair with up to FANOUT (start 5-10) later peaks (t2, f2) in a target zone: 1 <= dt <= 63 frames, optionally |f2 - f1| bounded.

**Hash:** pack into a uint32: `f1 (9 bits) << 15 | f2 (9 bits) << 6 | dt (6 bits)`. 24 bits used.

**Posting:** (track_id, t1), where t1 is the anchor frame index. Use dense internal track IDs (0..N-1), not raw FMA IDs, so deltas stay small.

**Matching:** for each query landmark (h, tq), fetch postings for h. For each (track, tdb), increment a counter keyed by (track, tdb - tq). Track score = max count over its offsets. Rank by score.

**Acceptance rule:** accept top-1 if score >= T_abs and score / second_best >= T_ratio. Calibrate on a validation split that includes negative (never-indexed) queries. Never tune on the test split.

**Size estimate (replace with measured values):** ~30 peaks/s x fanout 5 = ~150 landmarks/s, so ~4,500 per 30s clip, so ~480M postings for fma_large, so ~3.8 GB at 8 bytes per raw posting before compression. This is why partitioning and compact encoding are justified, not decorative.

---

## 8. Architecture

```
   client (CLI, later the demo page)
               |
               |  gRPC Identify(landmarks)      [Phase 5 adds an HTTP gateway
               v                                  that accepts audio uploads]
     +---------------------+
     |  coordinator (Go)   |  scatter-gather, deadlines, replica selection,
     +---------------------+  retry within budget, partial results, merge
         |       |       |
         |  gRPC Search, one replica per partition, concurrently
         v       v       v
      [P0/r0] [P1/r0] [P2/r0] ...
      [P0/r1] [P1/r1] [P2/r1] ...   replicas hold identical immutable segments
```

### Partitioning: document-partitioned by track (decided)

Each partition owns a disjoint set of tracks and holds all postings for those tracks.

Why:
- Scoring is per track (offset histogram), so a shard that owns all of a track's postings computes complete scores locally and returns only its top-k candidates. Responses are tiny.
- Term (hash-range) partitioning would ship raw postings back to the coordinator for aggregation: huge network cost. It also buys nothing on fan-out, because a query's hundreds of hashes spread uniformly over the hash space, so every query hits every partition anyway.
- Cost: every query fans out to all P partitions, so tail latency grows with P (Tail at Scale). Mitigations: deadlines, replica choice, hedging (stretch).

Assignment: `partition = track_id mod P` for balance. Decide whether segments store global or local IDs (`local = track_id / P`); log it in DECISIONS.md.

Optional experiment: implement term partitioning too and benchmark both. Strong interview story; only after Phase 4.

### Shard server
Loads one or more immutable segment files via mmap. Serves `Search(landmarks, top_k)` and returns local top-k candidates. Read-only at query time.

### Segment format v1 (immutable, little-endian)

```
Header    magic "LMRK", version u16, flags u16, n_keys u32, n_postings u64, n_tracks u32
Keys      n_keys x u32, sorted unique hash keys
Offsets   (n_keys + 1) x u64, byte offsets into the postings blob
Postings  per key: postings sorted by (track_id, t), varint-encoded deltas
Footer    crc32 over everything above
```

- Lookup: binary search Keys (mmap'd), read offsets, decode postings.
- Encoding within a key: track_id as a gap from the previous posting's track; t as raw varint when the track changes, as a delta when it doesn't. Sorted order means deltas are non-negative, so no zigzag needed.
- Keep a fixed-width baseline encoder too, so the compression result is a measured before/after.
- Stretch: Eytzinger layout or a sparse in-memory key index for cache efficiency, benchmarked against plain binary search.

### Ingest
Offline: list tracks, worker pool decodes and fingerprints, per-partition in-memory builders, sort, write segments. Resumable via a checkpoint of processed track IDs; idempotent. Track metadata (dense id, FMA id, title, artist, partition) goes in a small SQLite file that the coordinator loads.

### Coordinator
- `Identify(landmarks)`: fan out Search to one replica per partition concurrently, under a context deadline (start with a 200 ms total budget; tune).
- Replica selection: least outstanding requests. On error or timeout, retry the other replica if budget remains.
- Health: standard `grpc.health.v1` on every shard, polled; unhealthy replicas skipped.
- Merge: global top-k from per-partition top-k. Exact, because partitions are disjoint by track.
- Partial results: if a partition misses the deadline entirely, return the best of the rest with `partial = true` and `partitions_responded / partitions_total`. Document the correctness tradeoff.
- Stretch: hedged requests (second request to the other replica after a p95-based delay).

### Replication
RF = 2. Segments are immutable, so replication is copying identical files. No consensus protocol is needed on the read path. This is a deliberate simplification. Sean must be able to explain why Raft is unnecessary here and exactly when it becomes necessary (online writes, mutable index, membership changes).

### Cluster config
Static file (YAML or JSON) listing partitions and replica addresses for v1. Service discovery is a stretch.

### Observability
- Prometheus: per-shard search latency histogram, end-to-end identify latency histogram, fan-out errors and timeouts, partial-result count, postings scanned per query, QPS.
- Grafana dashboard JSON committed.
- Structured logs with `log/slog`.
- pprof endpoints on every binary (never publicly exposed).

---

## 9. gRPC API (sketch)

```proto
syntax = "proto3";
package landmark.v1;

message Landmark {
  uint32 hash = 1;
  uint32 t = 2;   // anchor frame index in the query
}

message Candidate {
  uint32 track_id = 1;
  uint32 score = 2;    // max offset-histogram count
  int32 offset = 3;    // best (t_db - t_query), in frames
}

message SearchRequest {
  repeated Landmark landmarks = 1;
  uint32 top_k = 2;
}

message SearchResponse {
  repeated Candidate candidates = 1;
  uint32 partition_id = 2;
  uint64 postings_scanned = 3;
}

service Shard {
  rpc Search(SearchRequest) returns (SearchResponse);
}

message IdentifyRequest {
  repeated Landmark landmarks = 1;
  uint32 top_k = 2;
}

message IdentifyResponse {
  repeated Candidate candidates = 1;
  bool matched = 2;               // passed acceptance thresholds
  bool partial = 3;
  uint32 partitions_responded = 4;
  uint32 partitions_total = 5;
}

service Coordinator {
  rpc Identify(IdentifyRequest) returns (IdentifyResponse);
}
```

Plus `grpc.health.v1` on every server. Generate code with buf.

Optimization to benchmark later: struct-of-arrays (`repeated fixed32 hashes`, `repeated uint32 times`) instead of repeated messages, to cut per-message overhead.

---

## 10. Tech stack

- **Go** (latest stable): `google.golang.org/grpc`, `google.golang.org/protobuf`, `gonum.org/v1/gonum/dsp/fourier` (FFT), `github.com/prometheus/client_golang`, `log/slog`, `golang.org/x/sync/errgroup`. mmap via `syscall.Mmap` behind a small interface (or `golang.org/x/exp/mmap`).
- **Python 3** (prototype and eval): numpy, scipy, librosa, soundfile, pandas, matplotlib, pytest, ruff. Use uv or venv.
- **ffmpeg** for decoding.
- **buf** for protobuf codegen.
- **Docker + docker compose** for the local cluster.
- **Load testing:** custom open-loop generator (`cmd/loadgen`), cross-checked with ghz.
- **Prometheus + Grafana** via compose.
- **GitHub Actions** CI: golangci-lint, `go vet`, `go test -race`, bounded fuzzing, ruff, pytest.
- **Deploy (Phase 5):** small cloud VM(s). If it's AWS EC2, AWS goes back on the resume honestly. Multi-node benchmarks: spin up several VMs for a session, tear down after.

---

## 11. Repo layout

```
landmark/
  CLAUDE.md
  README.md
  Makefile
  go.mod
  buf.yaml
  buf.gen.yaml
  .claude/settings.json          attribution off (section 18)
  proto/landmark/v1/landmark.proto
  cmd/
    ingest/                      build segments from audio
    shard/                       shard server
    coordinator/                 scatter-gather server
    identify/                    CLI: fingerprint a file, query coordinator
    loadgen/                     open-loop load generator
  internal/
    audio/                       ffmpeg decode
    fingerprint/                 STFT, peaks, landmarks, hashing
    segment/                     on-disk format writer/reader, varint codec
    index/                       in-memory builder, partition assignment
    match/                       offset-histogram scoring, top-k
    shard/                       Search handler
    coordinator/                 fan-out, replica selection, merge
    cluster/                     static config, health tracking
    metrics/
  prototype/                     Python reference implementation
  eval/                          query generation, degradations, scoring, CIs
    results/
  bench/
    scripts/
    results/                     raw outputs + machine spec, committed
  deploy/
    docker-compose.yml
    Dockerfile.shard
    Dockerfile.coordinator
    prometheus.yml
    grafana/
  web/                           demo page (Phase 5)
  docs/
    PROJECT_BRIEF.md             this file
    DECISIONS.md
    PROGRESS.md
    BENCHMARKS.md
    EVAL.md
    INTERVIEW_NOTES.md
    handoffs/
  data/                          gitignored: FMA zips and extracted audio
  .github/workflows/ci.yml
```

---

## 12. Phased plan

Phases are independently shippable. Phase 3 plus Phase 4 numbers are the minimum viable resume line.

### Phase 0: Setup (1 session)
- `go mod init`, Python env, ffmpeg check, Makefile targets (`build`, `test`, `lint`, `proto`, `cluster-up`, `cluster-down`).
- CI skeleton running.
- `.claude/settings.json` with attribution off; verify on the first commit.
- Seed `docs/DECISIONS.md` with D1-D5 (section 19) and create `docs/PROGRESS.md`.
- Download `fma_metadata` and `fma_small` after Sean confirms disk space; verify SHA1.

**Done when:** CI green on the skeleton, `make test` works, fma_small unpacked, `git log -1 --format=%B` shows no AI trailer.

### Phase 1: Python reference prototype (weeks 1-2)
- `prototype/fingerprint.py`, `index.py` (dict-based), `match.py`.
- Eval harness v1 (section 13): seeded query generator, degradations, negatives, Wilson CIs, threshold sweep.
- Measure peaks/s and landmarks/s.
- Export golden test vectors: landmark sets for ~20 tracks as JSON, for Go parity tests.

**Done when:** clean 10s excerpts identify at or near 100% (if not, the fingerprinter is broken; fix before moving on), and the accuracy-vs-condition table exists in `docs/EVAL.md`.

### Phase 2: Go core, single node (weeks 3-4)
- `internal/audio`, `internal/fingerprint` in Go. Parity test against the Python golden vectors: Jaccard similarity of landmark sets >= 0.98 (small float differences are expected; investigate anything lower).
- Benchmark Go vs Python fingerprint throughput (seconds of audio per second, single core and all cores).
- `internal/segment`: writer, mmap reader, varint codec. Round-trip property tests. Go native fuzz tests on the reader: corrupt input must return an error, never panic.
- `internal/match`, `cmd/shard` serving one segment, `cmd/identify` CLI.
- Measure index size (raw vs encoded) and single-shard query latency.

**Done when:** end-to-end identify on fma_small through one shard server matches the Python prototype's accuracy within CI bounds, and fuzzing runs in CI with a bounded `-fuzztime`.

### Phase 3: Distributed (weeks 4-5)
- Partitioned ingest (P partitions), cluster config, `cmd/coordinator` with scatter-gather, deadlines, merge, partial results.
- RF = 2, health checks, replica selection, retry within budget.
- docker compose cluster: P x RF shards, coordinator, Prometheus, Grafana.
- Integration test in CI on a tiny synthetic index.
- Scale to fma_medium, then fma_large (confirm disk first).

**Done when:** `kill -9` on a replica under load leaves queries succeeding (success rate and p99 during the event are measured), and partial results are flagged correctly when an entire partition is down.

### Phase 4: Measure (week 6)
- `cmd/loadgen`: open-loop, fixed arrival rate (Poisson optional), latency measured from the *scheduled* send time to avoid coordinated omission. Cross-check against ghz.
- Benchmarks (each: script in `bench/scripts/`, raw output in `bench/results/` with machine spec and commit SHA):
  - **B1** latency p50/p95/p99/p99.9 vs offered QPS, per partition count
  - **B2** max sustainable QPS under a p99 SLO (start at 100 ms) vs partitions (1, 2, 4, 8)
  - **B3** failure-injection timeline: success rate and p99 around a replica kill
  - **B4** index size: raw vs encoded, bytes per posting
  - **B5** ingest throughput: tracks/min vs worker count
- Eval at scale on fma_large with CIs, same conditions as Phase 1.
- Honest hardware caveat: if every process shares one machine, B2 measures parallelism, not distribution. Label it. For the real distributed numbers, rerun B1-B3 on separate VMs.

**Done when:** `docs/BENCHMARKS.md` has every chart with methodology, and every number traces to a results file.

### Phase 5: Ship (winter break)
- HTTP gateway and demo page: record ~10s from the mic (MediaRecorder), upload, server decodes with ffmpeg, returns title, artist, confidence. Never serves audio.
- Upload limits: max size, timeouts, rate limiting.
- Deploy: single VM, compose, TLS reverse proxy.
- README: what it is, architecture diagram, headline numbers (measured only), local run instructions, design decisions summary.

**Done when:** the public URL identifies a track from a phone in a noisy room.

### Phase 6: Stretch (pick by value)
- **DJ-mix tracklisting:** sliding windows (e.g., 10s window, 5s hop) over a mix, identify each window, smooth into segments (merge runs, minimum duration, handle crossfades where two tracks overlap), output a timestamped tracklist. Ground truth: Sean's own sets with known tracklists. Real users from Sean's audience are the goal.
- **Tempo/pitch robustness:** DJs speed tracks by a few percent and absolute (f, dt) hashes break. Options: index several speed variants (cheap, multiplies index size) or tempo-invariant hashing (Panako, quad-based). Evaluate accuracy vs speed change.
- **Hedged requests,** with B1 rerun to show the p99 effect.
- **Term vs document partitioning** benchmarked head to head.
- **Online ingest:** append segments plus background merge (LSM-style). Reintroduces consistency questions.
- **Hot-key cache** and Eytzinger key layout.

---

## 13. Evaluation methodology

- **Splits by track.** Positives sampled from indexed tracks; negatives from a held-out set that is never indexed. Validation split (threshold tuning) and test split (reported numbers) are disjoint, seeded, and their manifests are committed.
- **Excerpts:** 5s and 10s at random offsets.
- **Conditions:** clean; additive noise at SNR 15 / 10 / 5 / 0 dB (white and real noise); MP3 re-encode at 128k and 64k; speed +/-2% (expected v1 failure; document it).
- **Metrics:** top-1 accuracy on positives; false accept rate on negatives at the chosen threshold; both with Wilson 95% CIs. Latency is reported separately in benchmarks.
- **Sample size:** at least ~1,000 queries per condition, which keeps Wilson half-widths around 2-3 points.
- **Never tune on test.**

---

## 14. Benchmark methodology

- Record with every run: CPU model, cores, RAM, disk type, OS, Go version, commit SHA, cluster topology, dataset, index size, page-cache state (warm vs cold; drop caches for cold runs on Linux).
- Warm up 30s, measure 120s, 3 repetitions; report the median and the spread.
- Open-loop constant arrival rate; latency measured from the intended send time.
- Replay pre-fingerprinted queries so the benchmark measures the service, not ffmpeg.
- Change one variable at a time.

---

## 15. Target resume bullets (fill only with measured numbers)

● Built a distributed audio fingerprinting service in Go indexing [X]M landmark hashes from 106,000+ tracks across [N] document-partitioned gRPC shard servers with scatter-gather queries, sustaining [X] QPS at [Y] ms p99

● Designed a delta/varint-encoded, memory-mapped segment format cutting index size [X]%, and added shard replication with health-checked failover, holding [X]% query success with a replica killed under load

● Achieved [X]% top-1 identification accuracy at [Y] dB SNR (Wilson 95% CI, n = [N]) with [Z]% false accepts; shipped a DJ-mix track-ID tool on top used by [N] DJs

Rules: no number goes on the resume unless it exists in `bench/results/` or `eval/results/`. The second half of bullet 3 only ships if the stretch ships with real users.

---

## 16. Interview questions Sean must be able to answer

1. Why document partitioning instead of term partitioning here? When would term partitioning win?
2. Why does p99 get worse as partitions are added, even though each shard does less work? What do deadlines and hedging buy, and what do they cost?
3. Why mmap instead of `read()` into a buffer? What happens with a cold page cache, and how did you measure it?
4. Why does v1 replication need no consensus protocol? What changes if tracks can be added online?
5. What is coordinated omission, and how does your load generator avoid it?
6. How did you pick the acceptance threshold? What's the precision/recall tradeoff?
7. Where is the bottleneck at max QPS? (Answer with pprof evidence.)
8. How would you scale to 100M tracks? (Partitions per node, tiered storage, key-space size, hash collisions, bytes per posting.)
9. What happens to accuracy when a DJ speeds a track up 4%, and why?
10. Walk through one query end to end, with real numbers at each hop.

---

## 17. Session protocol

- **Start:** read `CLAUDE.md`, `docs/PROGRESS.md`, and the latest `docs/handoffs/` file if one exists. State the session plan in 2-3 lines.
- **During:** small commits, imperative subject lines, body explains why. Tests pass before every commit. No AI attribution.
- **End:** update `docs/PROGRESS.md` (done, next, blockers, open questions), add `DECISIONS.md` entries, leave the build green.

---

## 18. Claude Code configuration

Disable commit and PR attribution. `includeCoAuthoredBy` is deprecated; the current key is `attribution`. Put this in `~/.claude/settings.json` (applies to every project) and also commit it in this repo's `.claude/settings.json`:

```json
{
  "attribution": {
    "commit": "",
    "pr": ""
  }
}
```

Verify after the first commit: `git log -1 --format=%B` must show no trailer. If one appears, check `/status` for which settings files loaded, and amend the commit.

---

## 19. Seed decisions for DECISIONS.md

- **D1. Go for all services.** Default infra language; painless gRPC (C++ gRPC builds are a time sink); fast to learn from C; adds a language Sean can defend. Python stays for the prototype and eval.
- **D2. Document partitioning by track.** Complete per-track scores locally, tiny responses, exact merge. Cost: full fan-out per query. (Supersedes an earlier hash-range idea; see section 8.)
- **D3. Immutable segments, mmap'd.** Simple concurrency (read-only at query time), trivial replication, OS page cache does the caching. Cost: no online writes in v1.
- **D4. RF = 2, no consensus.** Immutable data makes consensus unnecessary on the read path. Revisit only with online ingest.
- **D5. Python prototype first, Go port second.** Validates the algorithm fast in Sean's strongest language, yields golden vectors for parity tests and a measured Go-vs-Python speedup.

---

## 20. Risks

- **Time.** Classes and recruiting come first. Mitigation: phases ship independently; Phase 3 + 4 is the minimum.
- **Disk.** fma_large needs ~93 GiB zipped plus about the same extracted. Never cache full PCM (~140 GB).
- **Ingest CPU time** on 106k MP3s. Parallelize, make resumable, measure on fma_small and extrapolate before committing to fma_large.
- **Single-machine benchmark credibility.** Label single-host results honestly; do real multi-node runs for the headline numbers.
- **Scope creep.** LLM features, Kubernetes, fancy UI are out until Phase 5 is done.
- **Corrupt FMA files.** Handle and count; never crash ingest.

---

## 21. Changelog

- 2026-10-14: Created. Partitioning set to document (by track) instead of hash range before any code was written (D2).
