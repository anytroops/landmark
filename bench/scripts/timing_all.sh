#!/usr/bin/env bash
# Every timing measurement still open, one after another (about 1.5 h):
# live monitoring capacity and a worker kill (D38-D40), the head-to-head's
# build and query cost (D41), and adding songs under load (D42). Each
# harness checks AC power, waits for the battery to stop charging, and
# marks a run invalid if power changed or the machine slept. Run it on an
# otherwise idle machine with the lid open.
#
#   bash bench/scripts/timing_all.sh <panako time filter chosen on val>
set -u
cd "$(dirname "$0")/../.."
panako=${1:?panako time filter}
go build -o bin/ ./cmd/... ./bench/h2hquery
bash bench/scripts/live_rt.sh
uv run python -m eval.h2h cost --density 80 --time-filter "$panako"
uv run python -m bench.scripts.live_writes --batches 10 --batch-size 100 --rate 200 \
  --out bench/results/livewrites_r200
