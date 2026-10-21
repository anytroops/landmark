#!/usr/bin/env bash
# Live monitoring in real time (D38-D40): capacity sweep, then a worker kill.
# Needs AC power, the lid open, and the battery done charging (the harness
# checks power and charge and holds caffeinate). About 45 minutes.
#
#   bash bench/scripts/live_rt.sh
#
# Each point: N stations, each playing 3 UnmixDB test mixes back to back in
# real time, monitored by one worker (capacity N). The kill run: 16
# stations on worker w0, w1 standing by, w0 killed (SIGKILL) 120 s in.
# A point that overloads or stalls exits nonzero; the next still runs.
set -u
cd "$(dirname "$0")/../.."
make build >/dev/null
for n in 16 24 32 48 64; do
  uv run python -m bench.scripts.live realtime --stations "$n" --per-station 3 \
    --workers 1 --capacity "$n" --out "bench/results/live/rt_cap${n}"
done
uv run python -m bench.scripts.live realtime --stations 16 --per-station 3 \
  --workers 2 --capacity 16 --kill-at 120 --out bench/results/live/rt_kill16
