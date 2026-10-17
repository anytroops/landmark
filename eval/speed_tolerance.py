"""Speed tolerance curve: identification vs tempo change (D31 step 1).

  python -m eval.speed_tolerance --tag _medium_speedtol --thresholds _medium_d30
  python -m eval.speed_tolerance --tag _medium_speedtol_stop0.04 --thresholds _medium_d30_stop0.04

Reads test-split positives run with speed_{x}pct (turntable: tempo and pitch
together) and stretch_{x}pct (keylock: tempo only) conditions. Per condition:
raw top-1 (Wilson 95% CI), accuracy at the thresholds calibrated for that
index and cutoff (summary_seed0{thresholds}.json, D13), and the median
fraction of each track's clean-query score that survives the change (paired
by track), which shows how fast the matching evidence decays.
"""

import argparse
import json
import re
from pathlib import Path

import matplotlib
import numpy as np
import pandas as pd

from eval.stats import wilson
from eval.summarize import accepted

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402

RES = Path(__file__).resolve().parent / "results"
ROOT = RES.parent.parent
NAME = re.compile(r"(speed|stretch)_([+-]\d+(?:\.\d+)?)pct_10s")


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--tag", required=True)
    ap.add_argument("--thresholds", required=True, help="tag whose summary gives t_abs, t_ratio")
    ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--figure", type=Path, help="PNG path (default: no figure)")
    args = ap.parse_args()

    th = json.loads((RES / f"summary_seed{args.seed}{args.thresholds}.json").read_text())
    t_abs, t_ratio = th["thresholds"]["t_abs"], th["thresholds"]["t_ratio"]
    d = pd.read_csv(RES / f"queries_test_seed{args.seed}{args.tag}.csv.gz")
    if "error" in d.columns:
        d = d[d["error"].isna()]
    d = d[d["label"] == "pos"].copy()
    d["ok"] = accepted(d, t_abs, t_ratio) & d["correct"].to_numpy()
    clean = d[d["cond"] == "clean_10s"].set_index("fma_id")["top1_score"]

    rows = []
    for cond, g in d.groupby("cond"):
        m = NAME.fullmatch(cond)
        kind, pct = ("clean", 0.0) if cond == "clean_10s" else (m.group(1), float(m.group(2)))
        n, raw, ok = len(g), int(g["correct"].sum()), int(g["ok"].sum())
        # Fraction of the clean score kept, for queries still answered correctly.
        hit = g[g["correct"]].set_index("fma_id")["top1_score"]
        kept = hit / clean.reindex(hit.index)
        rows.append({
            "cond": cond, "kind": kind, "pct": pct, "n": n,
            "raw_top1": raw / n, "raw_top1_ci": wilson(raw, n),
            "accuracy": ok / n, "accuracy_ci": wilson(ok, n),
            "median_score_kept": float(np.nanmedian(kept)) if len(kept) else 0.0,
        })  # fmt: skip
    rows.sort(key=lambda r: (r["kind"], r["pct"]))

    out = {"tag": args.tag, "thresholds_from": args.thresholds, "t_abs": t_abs,
           "t_ratio": t_ratio, "rows": rows}  # fmt: skip
    (RES / f"speedtol_seed{args.seed}{args.tag}.json").write_text(json.dumps(out, indent=1) + "\n")
    lines = [
        f"# Speed tolerance, {args.tag} (thresholds t_abs {t_abs}, t_ratio {t_ratio})",
        "",
        "| Condition | n | Raw top-1 (95% CI) | Accuracy at threshold (95% CI) "
        "| Clean score kept |",
        "|---|---|---|---|---|",
    ]
    for r in rows:
        lo, hi = r["raw_top1_ci"]
        alo, ahi = r["accuracy_ci"]
        lines.append(
            f"| {r['cond']} | {r['n']} | {100 * r['raw_top1']:.1f} ({100 * lo:.1f}-{100 * hi:.1f}) "
            f"| {100 * r['accuracy']:.1f} ({100 * alo:.1f}-{100 * ahi:.1f}) "
            f"| {100 * r['median_score_kept']:.0f}% |"
        )
    (RES / f"speedtol_seed{args.seed}{args.tag}.md").write_text("\n".join(lines) + "\n")
    print("\n".join(lines))

    if args.figure:
        fig, (ax_a, ax_s) = plt.subplots(1, 2, figsize=(11, 4))
        base = next(r for r in rows if r["kind"] == "clean")
        kinds = (("speed", "turntable (tempo + pitch)"), ("stretch", "keylock (tempo only)"))
        for kind, label in kinds:
            rs = sorted([r for r in rows if r["kind"] == kind] + [base], key=lambda r: r["pct"])
            xs = [r["pct"] for r in rs]
            acc = [100 * r["accuracy"] for r in rs]
            raw = [100 * r["raw_top1"] for r in rs]
            ax_a.plot(xs, acc, "o-", ms=3, label=f"{label}, at threshold")
            ax_a.plot(xs, raw, "--", lw=1, label=f"{label}, raw top-1")
            ax_s.plot(xs, [100 * r["median_score_kept"] for r in rs], "o-", ms=3, label=label)
        ax_a.set(xlabel="tempo change (%)", ylabel="% of queries", ylim=(0, 102),
                 title="Identification vs tempo change")  # fmt: skip
        ax_s.set(xlabel="tempo change (%)", ylabel="median % of clean score kept", ylim=(0, 102),
                 title="Matching evidence that survives")  # fmt: skip
        for ax in (ax_a, ax_s):
            ax.grid(alpha=0.3)
            ax.legend(fontsize=7)
        fig.suptitle(f"fma_medium test positives (n=1,000 per point), {args.tag}", fontsize=9)
        fig.tight_layout()
        args.figure.parent.mkdir(parents=True, exist_ok=True)
        fig.savefig(args.figure, dpi=130)
        print(args.figure)


if __name__ == "__main__":
    main()
