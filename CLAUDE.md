# Landmark

Distributed audio fingerprinting and identification service in Go (Shazam / Content ID style). Audio is the domain; distributed systems is the work.

Full context, spec, plan, and methodology: `docs/PROJECT_BRIEF.md`. Read it fully in the first session. At the start of every session, read `docs/PROGRESS.md` and the latest file in `docs/handoffs/` if one exists.

## Hard rules

- Never add "Co-Authored-By: Claude", "Generated with Claude Code", or any AI attribution to commits, PR titles or descriptions, code comments, or docs.
- Never use em dashes anywhere: code comments, docs, commit messages, chat. Use commas, colons, periods, or parentheses.
- No fabricated numbers. Performance and accuracy figures come only from committed runs in `bench/results/` or `eval/results/`. Otherwise write `[TBD]`.
- Ask before downloading anything over ~5 GB. Never download fma_full.
- Never commit audio, datasets, decoded PCM, or built index segments.
- If context gets significantly degraded or compacted, say so explicitly and write `docs/handoffs/YYYY-MM-DD.md` (state, next steps, open questions, gotchas) before continuing.

## Working mode

- Write freely: scaffolding, Makefile, CI, Docker and compose, proto boilerplate, tests, bench and eval scripts, plots.
- Core components (fingerprint, match, segment format, shard search path, coordinator fan-out and failover, replication): propose 2-3 options with tradeoffs first, wait for Sean's pick, then implement in small diffs and explain the non-obvious parts.
- After each core component: ask Sean 3 interview-style questions about it and log the Q&A in `docs/INTERVIEW_NOTES.md`.
- Log every non-trivial decision in `docs/DECISIONS.md`: context, options, decision, consequences, date.
- If Sean says "just write it," do it, but still log the decision and still ask the questions.

## About Sean

- Fluent in Python, C/C++, DSP (STFT, spectral analysis). Do not over-explain DSP.
- New to Go, gRPC, and distributed systems in practice. Has not taken an OS/systems course, so explain systems concepts (mmap, page cache, syscalls, memory layout, concurrency, tail latency) concretely the first time they come up.
- ~10 hrs/week, often short sessions. Recruiting comes first.
- Wants direct, blunt, specific communication. No filler, no hype.

## Conventions

- Go: gofmt, golangci-lint, `go vet`. Wrap errors with `%w`. `context.Context` as the first parameter on anything doing I/O or RPC. No package-level mutable state. Table-driven tests. `go test -race` must pass.
- Python (`prototype/`, `eval/`): ruff, pytest, type hints, seeded randomness.
- Commits: small, imperative subject under 72 chars, body explains why. Tests pass before every commit.

## Commands

Fill these in during Phase 0.

- `make build`, `make test`, `make lint`, `make proto`
- `make cluster-up`, `make cluster-down`

## Session protocol

- Start: read `docs/PROGRESS.md` and the latest handoff; state the session plan in 2-3 lines.
- End: update `docs/PROGRESS.md` (done, next, blockers, open questions), add `DECISIONS.md` entries, leave the build green.
