# Progress

## Current phase: 0 (Setup)

## Done
- 2026-10-14: Repo created. Go module, buf proto + generated gRPC stubs, Makefile, golangci-lint, uv Python env, CI workflow, compose stack (Prometheus + Grafana), docs skeleton, seed decisions D1-D8.
- 2026-10-14: Local toolchain installed via Homebrew: Go, buf, protoc, golangci-lint, ruff, uv, ffmpeg, Colima + docker CLI + compose.

## Next
- Confirm CI is green on GitHub.
- Verify fma_metadata and fma_small SHA1, unzip (fall back to 7zip if unzip fails), delete zips.
- Phase 1: Python prototype (`prototype/fingerprint.py`, `index.py`, `match.py`), eval harness v1.

## Blockers
- None.

## Open questions
- fma_large needs ~186 GB (zip + extracted). Laptop has ~113 GiB free. Options: external SSD, delete zip mid-extract via streaming unzip, or run Phase 3-4 at scale on a cloud VM. Decide before Phase 3.
