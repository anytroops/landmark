"""Plot B3 failure timelines: per-second p99 and success, kill marked.

python -m bench.scripts.plot_b3 docs/figures/b3_rate100.png bench/results/b3_rate100_rep*
"""

import gzip
import json
import sys
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402


def main() -> None:
    out, dirs = Path(sys.argv[1]), [Path(d) for d in sys.argv[2:]]
    fig, (ax_lat, ax_ok) = plt.subplots(2, 1, figsize=(10, 6), sharex=True)
    for d in dirs:
        s = json.loads((d / "summary.json").read_text())
        t = [b["start_s"] for b in s["timeline"]]
        ax_lat.plot(t, [b["p99_ms"] for b in s["timeline"]], marker=".", label=f"{d.name} p99")
        ax_ok.plot(t, [b["success_rate"] for b in s["timeline"]], marker=".", label=d.name)
        slow = 0
        with gzip.open(d / "requests.csv.gz", "rt") as f:
            next(f)
            for line in f:
                due, lat = (float(x) for x in line.split(",")[:2])
                slow += lat > 50 and due >= s["config"]["warmup_ns"] / 1e6
        print(f"{d.name}: {slow} measured requests over 50 ms; kill at {s['event']['at_s']:.2f} s")
    kill = json.loads((dirs[0] / "summary.json").read_text())["event"]["at_s"]
    for ax in (ax_lat, ax_ok):
        ax.axvline(kill, color="red", ls="--", lw=1)
        ax.grid(alpha=0.3)
    ax_lat.text(kill, ax_lat.get_ylim()[1] * 0.9, " kill -9 replica", color="red", fontsize=8)
    ax_lat.set(ylabel="p99 latency per second (ms)", yscale="log")
    ax_ok.set(ylabel="complete answers / sent", xlabel="seconds since start", ylim=(0, 1.05))
    ax_lat.legend(fontsize=7)
    m = json.loads((dirs[0] / "summary.json").read_text())
    fig.suptitle(f"B3: {m['config']['label']}; {m['config']['rate_qps']:g} qps; "
                 f"{m['machine']['cpu']}, power={m['machine']['power']}", fontsize=9)  # fmt: skip
    fig.tight_layout()
    out.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(out, dpi=130)
    print(out)


if __name__ == "__main__":
    main()
