#!/usr/bin/env bash
# B3: failure-injection timeline. Runs open-loop load against the compose
# cluster and kill -9s one replica mid-run, then restarts it.
#
#   bench/scripts/b3_failover.sh <label> [rate_qps] [kill_at_s] [victim]
#
# Requires: cluster up (make cluster-up), bin/loadgen built, query file at
# data/queries/test_10s_seed0.gob (bin/loadgen queries -out ...).
# Writes bench/results/b3_<label>/{summary.json,requests.csv.gz}.
# Timings are only comparable on AC power; summary.json records the source.
set -euo pipefail
label=${1:?label}
rate=${2:-100}
kill_at=${3:-30}
victim=${4:-landmark-shard-1a-1}
out=bench/results/b3_${label}
compose="docker compose -f deploy/docker-compose.yml"

$compose start shard-0a shard-0b shard-1a shard-1b >/dev/null
sleep 3 # let the coordinator's health loop see every replica

bin/loadgen run \
  -queries data/queries/test_10s_seed0.gob \
  -rate "$rate" -warmup 10s -duration 60s \
  -event-at "${kill_at}s" -event-cmd "docker kill -s KILL $victim" \
  -label "B3 single-host compose, P=2 RF=2, kill -9 $victim at ${kill_at}s" \
  -out "$out"

svc=${victim#landmark-} # container landmark-shard-1a-1 -> service shard-1a
svc=${svc%-1}
$compose start "$svc" >/dev/null
echo "wrote $out"
