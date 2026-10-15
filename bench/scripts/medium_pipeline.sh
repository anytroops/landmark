#!/usr/bin/env bash
# Build everything fma_medium needs, end to end:
#   dedupe -> splits -> Go ingest (P=1,2,4,8 from one fingerprint pass)
#   -> accuracy eval through a Go shard (val + test) -> loadgen query file.
# Requires data/fma_medium (and fma_metadata), bin/ built, AC power.
# caffeinate keeps the Mac awake for the ~45 minute run.
set -euo pipefail
cd "$(dirname "$0")/../.."
log() { echo "[$(date +%H:%M:%S)] $*"; }

log "dedupe (all-vs-all over 25,000 tracks)"
uv run python -m eval.dedupe --subset medium --threshold 25
log "splits"
uv run python -m eval.splits --subset medium
log "ingest P=1,2,4,8"
bin/ingest -manifest eval/manifests/splits_medium_seed0.json -out data/segments/medium -partitions 1,2,4,8

log "eval through a Go shard (P=1)"
bin/shard -segment data/segments/medium/p1/part-000.lmrk -listen 127.0.0.1:7201 -admin 127.0.0.1:9201 \
  > data/segments/medium/eval_shard.log 2>&1 &
shard=$!
trap 'kill $shard 2>/dev/null || true' EXIT
until grep -q serving data/segments/medium/eval_shard.log; do sleep 0.2; done
for split in val test; do
  uv run python -m eval.run_eval --manifest eval/manifests/splits_medium_seed0.json \
    --split "$split" --tag _medium --shard 127.0.0.1:7201
done
kill $shard
uv run python -m eval.summarize --tag _medium

log "loadgen query file"
bin/loadgen queries -manifest eval/manifests/splits_medium_seed0.json \
  -out data/queries/medium_test_10s_seed0.gob
log "done"
