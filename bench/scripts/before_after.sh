#!/usr/bin/env bash
# Before/after D27 (radix vote sort) + D28 (packed request arrays) at P=1 on fma_medium.
# Old binaries: commit 8f76046 (just before D27) in ../landmark-pre-d27. New: this repo's bin/.
set -euo pipefail
cd ~/Desktop/landmark
OLD=../landmark-pre-d27/bin
Q=data/queries/medium_test_10s_seed0.gob
IDX=data/segments/medium/p1
# 1. Latency at the same light load.
LANDMARK_BIN=$OLD caffeinate -dims uv run python -u -m bench.scripts.benchcluster sweep --index $IDX --queries $Q \
  --rates 100 --warmup 30 --duration 120 --reps 3 --out bench/results/before_after_medium/before
caffeinate -dims uv run python -u -m bench.scripts.benchcluster sweep --index $IDX --queries $Q \
  --rates 100 --warmup 30 --duration 120 --reps 3 --out bench/results/before_after_medium/after
# 2. Capacity: max rate with >= 99.9% answered within the 200 ms deadline.
LANDMARK_BIN=$OLD caffeinate -dims uv run python -u -m bench.scripts.b2_slo --index $IDX --queries $Q \
  --p99-ms 200 --lo 50 --hi 600 --out bench/results/b2_capacity_medium_before
caffeinate -dims uv run python -u -m bench.scripts.b2_slo --index $IDX --queries $Q \
  --p99-ms 200 --lo 300 --hi 1200 --out bench/results/b2_capacity_medium_after
