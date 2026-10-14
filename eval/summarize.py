"""Calibrate acceptance thresholds on val, report test with Wilson CIs.

Metrics per condition:
  raw_top1     top-1 correct ignoring thresholds (positives)
  accuracy     top-1 correct AND accepted (positives); a rejection is a miss
  far          accepted negatives / negatives
Threshold choice (D13): among (t_abs, t_ratio) with pooled val FAR <= max_far,
maximize mean val accuracy over conditions, excluding the speed conditions
(an expected v1 failure that would otherwise drag thresholds down to scrape
a few hits). Test is never used for tuning.
"""

import argparse
import json
from pathlib import Path

import numpy as np
import pandas as pd

from eval.stats import wilson

ROOT = Path(__file__).resolve().parent.parent
RES = ROOT / "eval" / "results"
T_ABS = list(range(2, 61))
T_RATIO = [1.0, 1.25, 1.5, 2.0, 2.5, 3.0, 4.0]
EXCLUDE_FROM_OBJECTIVE = ("speed_",)


def accepted(df: pd.DataFrame, t_abs: int, t_ratio: float) -> np.ndarray:
    col = "runner_up_score" if "runner_up_score" in df else "top2_score"  # pre-D14 runs
    second = np.maximum(df[col].to_numpy(), 1)
    s = df["top1_score"].to_numpy()
    return (s >= t_abs) & (s >= t_ratio * second)


def calibrate(val: pd.DataFrame, max_far: float) -> tuple[int, float, float, float]:
    tuned = ~val["cond"].str.startswith(EXCLUDE_FROM_OBJECTIVE)
    pos, neg = val[(val.label == "pos") & tuned], val[val.label == "neg"]
    best: tuple[float, int, float, float] | None = None
    for ta in T_ABS:
        for tr in T_RATIO:
            far = float(accepted(neg, ta, tr).mean())
            if far > max_far:
                continue
            ok = accepted(pos, ta, tr) & pos["correct"].to_numpy()
            acc = float(pd.Series(ok).groupby(pos["cond"].to_numpy()).mean().mean())
            if best is None or acc > best[0]:
                best = (acc, ta, tr, far)
    if best is None:
        raise SystemExit(f"no threshold meets FAR <= {max_far}")
    acc, ta, tr, far = best
    return ta, tr, acc, far


def table(df: pd.DataFrame, t_abs: int, t_ratio: float) -> list[dict[str, object]]:
    rows = []
    for cond, g in df.groupby("cond", sort=False):
        pos, neg = g[g.label == "pos"], g[g.label == "neg"]
        acc_k = int((accepted(pos, t_abs, t_ratio) & pos["correct"].to_numpy()).sum())
        raw_k = int(pos["correct"].sum())
        far_k = int(accepted(neg, t_abs, t_ratio).sum())
        rows.append(
            {
                "cond": cond,
                "n_pos": len(pos),
                "raw_top1": raw_k / len(pos),
                "raw_top1_ci": wilson(raw_k, len(pos)),
                "accuracy": acc_k / len(pos),
                "accuracy_ci": wilson(acc_k, len(pos)),
                "n_neg": len(neg),
                "far": far_k / len(neg),
                "far_ci": wilson(far_k, len(neg)),
                "median_postings_scanned": float(g["postings_scanned"].median()),
            }
        )
    return rows


def pct(x: float) -> str:
    return f"{100 * x:.1f}"


def markdown(rows: list[dict[str, object]]) -> str:
    out = [
        "| Condition | Top-1 raw (95% CI) | Accuracy at threshold (95% CI) "
        "| FAR (95% CI) | n pos / neg |",
        "|---|---|---|---|---|",
    ]
    for r in rows:
        lo, hi = r["raw_top1_ci"]
        alo, ahi = r["accuracy_ci"]
        flo, fhi = r["far_ci"]
        out.append(
            f"| {r['cond']} | {pct(r['raw_top1'])} ({pct(lo)}-{pct(hi)}) "
            f"| {pct(r['accuracy'])} ({pct(alo)}-{pct(ahi)}) "
            f"| {pct(r['far'])} ({pct(flo)}-{pct(fhi)}) | {r['n_pos']} / {r['n_neg']} |"
        )
    return "\n".join(out)


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--max-far", type=float, default=0.001)
    ap.add_argument("--tag", default="")
    args = ap.parse_args()

    def load(split: str) -> tuple[pd.DataFrame, pd.DataFrame]:
        df = pd.read_csv(RES / f"queries_{split}_seed{args.seed}{args.tag}.csv.gz")
        errors = df[df["error"].notna()] if "error" in df else df.iloc[:0]
        return df.drop(errors.index), errors

    val, val_err = load("val")
    test, test_err = load("test")
    t_abs, t_ratio, val_acc, val_far = calibrate(val, args.max_far)
    rows = table(test, t_abs, t_ratio)
    summary = {
        "thresholds": {"t_abs": t_abs, "t_ratio": t_ratio, "max_far": args.max_far},
        "val": {"mean_accuracy": val_acc, "pooled_far": val_far},
        "skipped": {"val": len(val_err), "test": len(test_err)},
        "test": rows,
    }
    (RES / f"summary_seed{args.seed}{args.tag}.json").write_text(
        json.dumps(summary, indent=2) + "\n"
    )
    md = markdown(rows)
    (RES / f"summary_seed{args.seed}{args.tag}.md").write_text(md + "\n")
    print(json.dumps(summary["thresholds"]), json.dumps(summary["val"]), summary["skipped"])
    print(md)


if __name__ == "__main__":
    main()
