"""Plot B2: maximum throughput under the latency SLO, per partition count.

python -m bench.scripts.plot_b2 bench/results/b2_medium docs/figures/b2_medium.png
"""

import json
import statistics
import sys
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402


def main() -> None:
    root, out = Path(sys.argv[1]), Path(sys.argv[2])
    rows = []
    for res in sorted(root.glob("P*/result.json"), key=lambda p: int(p.parent.name[1:])):
        r = json.loads(res.read_text())
        rate = r["max_qps_under_slo"]
        confirm = [p for p in r["probes"] if p["tag"].startswith("confirm") and p["rate"] == rate]
        rows.append(
            {
                "P": r["partitions"],
                "qps": rate,
                "p50": statistics.median(p["p50_ms"] for p in confirm),
                "p99": statistics.median(p["p99_ms"] for p in confirm),
                "p99_range": (min(p["p99_ms"] for p in confirm), max(p["p99_ms"] for p in confirm)),
            }
        )
        print(rows[-1])
    fig, (ax_q, ax_l) = plt.subplots(1, 2, figsize=(10, 4))
    xs = [str(r["P"]) for r in rows]
    ax_q.bar(xs, [r["qps"] for r in rows], color="C0")
    for x, r in zip(xs, rows, strict=True):
        ax_q.text(x, r["qps"], f"{r['qps']:.0f}", ha="center", va="bottom", fontsize=9)
    ax_q.set(xlabel="partitions (P)", ylabel="max qps under SLO",
             title="Max sustainable throughput")  # fmt: skip
    ax_l.plot(xs, [r["p50"] for r in rows], "o--", label="p50 at that load")
    ax_l.plot(xs, [r["p99"] for r in rows], "o-", label="p99 at that load")
    ax_l.axhline(100, color="gray", ls=":", lw=0.8)
    ax_l.set(xlabel="partitions (P)", ylabel="latency (ms)", title="Latency at max load")
    ax_l.legend(fontsize=8)
    for ax in (ax_q, ax_l):
        ax.grid(alpha=0.3)
    first = json.loads(next(root.glob("P*/confirm_rep0_r*/summary.json")).read_text())
    host = f"{first['machine']['cpu']}, power={first['machine']['power']}"
    slo = "SLO p99 <= 100 ms and >= 99.9% complete"
    fig.suptitle(f"B2, {root.name}: {slo}; single host ({host})", fontsize=9)
    fig.tight_layout()
    out.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(out, dpi=130)
    (root / "summary.json").write_text(json.dumps(rows, indent=1) + "\n")
    print(out)


if __name__ == "__main__":
    main()
