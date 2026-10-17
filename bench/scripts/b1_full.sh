#!/usr/bin/env bash
# B1, full methodology: latency vs offered load at 25/50/75/100% of each
# partition count's confirmed B2 rate (bench/results/b2_medium/P*/result.json),
# 30 s warmup, 120 s measured, 3 reps per point. About 30 minutes per P.
set -euo pipefail
cd "$(dirname "$0")/../.."
for P in 1 2 4 8; do
  max=$(python3 -c "import json;print(json.load(open('bench/results/b2_medium/P$P/result.json'))['max_qps_under_slo'])")
  rates=$(python3 -c "print(' '.join(str(round($max*f)) for f in (0.25,0.5,0.75,1.0)))")
  echo "P=$P B2 max $max qps -> rates $rates"
  caffeinate -dims uv run python -u -m bench.scripts.benchcluster sweep \
    --index "data/segments/medium/p$P" --queries data/queries/medium_test_10s_seed0.gob \
    --rates $rates --warmup 30 --duration 120 --reps 3 --out bench/results/b1_full_medium
done
