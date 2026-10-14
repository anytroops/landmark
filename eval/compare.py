"""Paired comparison of two eval runs on the same test queries (exact McNemar).

Each run is scored at its own val-calibrated thresholds. Queries are paired by
(fma_id, cond); both runs use the same seeded excerpts.
"""

import argparse
import json
from pathlib import Path

import pandas as pd
from scipy.stats import binomtest

RES = Path(__file__).resolve().parent / "results"


def accepted_correct(tag: str, seed: int) -> pd.Series:
    d = pd.read_csv(RES / f"queries_test_seed{seed}{tag}.csv.gz")
    d = d[d["error"].isna() & (d["label"] == "pos")]
    th = json.loads((RES / f"summary_seed{seed}{tag}.json").read_text())["thresholds"]
    ok = (
        (d["top1_score"] >= th["t_abs"])
        & (d["top1_score"] >= th["t_ratio"] * d["runner_up_score"].clip(lower=1))
        & d["correct"]
    )
    return ok.set_axis(pd.MultiIndex.from_frame(d[["fma_id", "cond"]]))


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("a")
    ap.add_argument("b")
    ap.add_argument("--seed", type=int, default=0)
    args = ap.parse_args()

    a, b = accepted_correct(args.a, args.seed), accepted_correct(args.b, args.seed)
    a, b = a.align(b, join="inner")
    lines = [f"# {args.b} vs {args.a}, test split, exact McNemar", ""]
    lines += [
        "| Condition | n | acc A | acc B | B-only | A-only | p |",
        "|---|---|---|---|---|---|---|",
    ]
    for cond in sorted(a.index.get_level_values("cond").unique()):
        x, y = a.xs(cond, level="cond"), b.xs(cond, level="cond")
        b_only, a_only = int((~x & y).sum()), int((x & ~y).sum())
        n_disc = a_only + b_only
        p = binomtest(b_only, n_disc, 0.5).pvalue if n_disc else 1.0
        lines.append(
            f"| {cond} | {len(x)} | {100 * x.mean():.1f} | {100 * y.mean():.1f} "
            f"| {b_only} | {a_only} | {p:.3g} |"
        )
    out = RES / f"compare_seed{args.seed}{args.a}_vs{args.b}.md"
    out.write_text("\n".join(lines) + "\n")
    print("\n".join(lines))


if __name__ == "__main__":
    main()
