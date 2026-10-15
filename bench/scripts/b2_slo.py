"""B2: maximum sustainable throughput under a latency SLO, per partition count.

  python -m bench.scripts.b2_slo --index data/segments/medium/p4 --lo 100 --hi 2000 \
      --queries data/queries/medium_test_10s_seed0.gob --out bench/results/b2_medium

SLO: p99 <= --p99-ms (default 100) and complete answers >= --min-success
(default 0.999), latency measured open loop from scheduled send times.

Search: bisect offered load between --lo (must pass) and --hi (must fail)
with short probes until the bracket is within --tolerance; then confirm the
best passing rate with the full methodology (30 s warmup, 120 s measured,
3 reps), stepping down 5% at a time until all reps pass. The reported
number is the confirmed rate. Single host: this measures parallelism on
one machine, not distribution.
"""

import argparse
import json
import os
from pathlib import Path

from bench.scripts.benchcluster import (
    cluster,
    require_ac_power,
    require_clean_tree,
    run_loadgen,
)

ROOT = Path(__file__).resolve().parents[2]


def passes(m: dict, p99_ms: float, min_success: float) -> bool:
    return m["p99_ms"] <= p99_ms and m["success_rate"] >= min_success


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--index", required=True)
    ap.add_argument("--queries", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--lo", type=float, required=True)
    ap.add_argument("--hi", type=float, required=True)
    ap.add_argument("--tolerance", type=float, default=0.05, help="relative bracket width")
    ap.add_argument("--p99-ms", type=float, default=100)
    ap.add_argument("--min-success", type=float, default=0.999)
    ap.add_argument("--probe-warmup", type=int, default=10)
    ap.add_argument("--probe-duration", type=int, default=30)
    ap.add_argument("--warmup", type=int, default=30)
    ap.add_argument("--duration", type=int, default=120)
    ap.add_argument("--reps", type=int, default=3)
    ap.add_argument("--deadline-ms", type=int, default=200)
    ap.add_argument("--workers", type=int, default=os.cpu_count() or 1)
    args = ap.parse_args()
    require_ac_power()
    require_clean_tree()

    index = Path(args.index)
    n_parts = json.loads((index / "index.json").read_text())["n_partitions"]
    out = Path(args.out) / f"P{n_parts}"
    label = (f"B2 single-host bare metal, P={n_parts} RF=1, SLO p99<={args.p99_ms:g} ms "
             f"and success>={args.min_success}")  # fmt: skip
    log: list[dict] = []
    with cluster(index, 1, args.deadline_ms, args.workers) as (target, _):

        def probe(rate: float, warmup: int, duration: int, tag: str) -> dict:
            s = run_loadgen(target, Path(args.queries), rate, warmup, duration,
                            out / f"{tag}_r{rate:.0f}", label, [])  # fmt: skip
            m = s["measured"]
            ok = passes(m, args.p99_ms, args.min_success)
            log.append({"tag": tag, "rate": rate, "pass": ok, "p99_ms": m["p99_ms"],
                        "p50_ms": m["p50_ms"], "success": m["success_rate"]})  # fmt: skip
            print(f"P={n_parts} {tag} rate={rate:.0f}: {'PASS' if ok else 'fail'} "
                  f"p50={m['p50_ms']} p99={m['p99_ms']} success={m['success_rate']:.4f}",
                  flush=True)  # fmt: skip
            return m

        lo, hi = args.lo, args.hi
        if not passes(probe(lo, args.probe_warmup, args.probe_duration, "probe"),
                      args.p99_ms, args.min_success):  # fmt: skip
            raise SystemExit(f"--lo {lo} already fails the SLO")
        if passes(probe(hi, args.probe_warmup, args.probe_duration, "probe"),
                  args.p99_ms, args.min_success):  # fmt: skip
            raise SystemExit(f"--hi {hi} passes the SLO; raise it")
        while (hi - lo) / lo > args.tolerance:
            mid = round((lo + hi) / 2)
            m = probe(mid, args.probe_warmup, args.probe_duration, "probe")
            lo, hi = (mid, hi) if passes(m, args.p99_ms, args.min_success) else (lo, mid)

        rate = lo
        while True:
            reps = [probe(rate, args.warmup, args.duration, f"confirm_rep{k}")
                    for k in range(args.reps)]  # fmt: skip
            if all(passes(m, args.p99_ms, args.min_success) for m in reps):
                break
            rate = round(rate * 0.95)

    result = {"partitions": n_parts, "max_qps_under_slo": rate, "slo_p99_ms": args.p99_ms,
              "slo_min_success": args.min_success, "probes": log}  # fmt: skip
    (out / "result.json").write_text(json.dumps(result, indent=1) + "\n")
    print(f"P={n_parts}: max sustainable {rate:.0f} qps under the SLO", flush=True)


if __name__ == "__main__":
    main()
