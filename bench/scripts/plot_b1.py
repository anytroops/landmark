"""Plot B1 (latency and success vs offered load, per partition count).

  python -m bench.scripts.plot_b1 bench/results/b1_quick_small docs/figures/b1_quick_small.png

Reads every <dir>/P*_rf*_r*_rep*/summary.json. With several reps per point,
plots the median and draws the min-max range.
"""

import json
import re
import statistics
import sys
from collections import defaultdict
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402

NAME = re.compile(r"P(\d+)_rf(\d+)_r([\d.]+)_rep(\d+)")


def load(root: Path) -> dict[int, dict[float, list[dict]]]:
    runs: dict[int, dict[float, list[dict]]] = defaultdict(lambda: defaultdict(list))
    for summary in sorted(root.glob("*/summary.json")):
        m = NAME.fullmatch(summary.parent.name)
        if m:
            p, rate = int(m.group(1)), float(m.group(3))
            runs[p][rate].append(json.loads(summary.read_text())["measured"])
    return runs


def main() -> None:
    root, out = Path(sys.argv[1]), Path(sys.argv[2])
    runs = load(root)
    fig, (ax_lat, ax_ok) = plt.subplots(1, 2, figsize=(12, 4.5))
    for i, p in enumerate(sorted(runs)):
        color = f"C{i}"  # one color per partition count: dashed p50, solid p99
        rates = sorted(runs[p])
        for key, style in (("p50_ms", "--"), ("p99_ms", "-")):
            vals = [[r[key] for r in runs[p][x]] for x in rates]
            med = [statistics.median(v) for v in vals]
            line = ax_lat.plot(
                rates, med, style, color=color, marker="o", ms=3, label=f"P={p} {key[:3]}"
            )[0]
            if any(len(v) > 1 for v in vals):
                ax_lat.fill_between(rates, [min(v) for v in vals], [max(v) for v in vals],
                                    color=line.get_color(), alpha=0.15)  # fmt: skip
        ok = [statistics.median(r["success_rate"] for r in runs[p][x]) for x in rates]
        ax_ok.plot(rates, ok, color=color, marker="o", ms=3, label=f"P={p}")
    ax_lat.set(xscale="log", yscale="log", xlabel="offered load (queries/s)", ylabel="latency (ms)",
               title="Latency from scheduled send time")  # fmt: skip
    ax_lat.axhline(200, color="gray", lw=0.8, ls=":")
    ax_lat.text(ax_lat.get_xlim()[0], 205, " 200 ms deadline", color="gray", fontsize=8)
    ax_ok.set(xscale="log", xlabel="offered load (queries/s)", ylabel="complete answers / sent",
              title="Success rate", ylim=(0, 1.02))  # fmt: skip
    for ax in (ax_lat, ax_ok):
        ax.grid(True, which="both", alpha=0.3)
        ax.legend(fontsize=7, ncol=2)
    first = json.loads(next(root.glob("*/summary.json")).read_text())
    where = first["config"]["label"].split(",")[0]
    machine = f"{first['machine']['cpu']}, power={first['machine']['power']}"
    fig.suptitle(f"B1, {root.name}: {where}; {machine}", fontsize=9)
    fig.tight_layout()
    out.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(out, dpi=130)
    print(out)


if __name__ == "__main__":
    main()
