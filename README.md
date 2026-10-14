# Landmark

[![ci](https://github.com/anytroops/landmark/actions/workflows/ci.yml/badge.svg)](https://github.com/anytroops/landmark/actions/workflows/ci.yml)

A distributed audio fingerprinting and identification service in Go, in the style of Shazam and YouTube Content ID. It uses landmark (constellation) hashing, a document-partitioned index of mmap'd immutable segments, and a gRPC coordinator that scatter-gathers queries across replicated shards.

**Status:** Phase 0 (setup). Nothing works end to end yet.

## Architecture

```
   client
     |  gRPC Identify(landmarks)
     v
 coordinator     scatter-gather, deadlines, replica selection, merge
   |    |    |
   v    v    v
 [P0] [P1] [P2] ...   shards, each partition replicated (RF = 2)
```

Design rationale: [docs/DECISIONS.md](docs/DECISIONS.md).

## Results

Accuracy and latency figures are published only from committed runs.

| Metric | Value |
|---|---|
| Tracks indexed | [TBD] |
| p99 identify latency | [TBD] |
| Top-1 accuracy at 5 dB SNR | [TBD] |

## Development

Requires Go, buf, golangci-lint, uv, ffmpeg, and Docker with compose.

```bash
make test         # go test -race + pytest
make lint         # go vet, golangci-lint, buf lint, ruff
make build
make proto        # regenerate gen/ from proto/
make cluster-up   # Prometheus on :9090, Grafana on :3000
```

Data comes from [FMA](https://github.com/mdeff/fma) and lives in the gitignored `data/` directory. The repo stores fingerprints and metadata only, never audio.

## License

MIT
