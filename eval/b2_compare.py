"""Compare eval.b2_eval runs of triplet fingerprint options (docs/design/triplet-noise.md).

  python -m eval.b2_compare grid _tn_val_base _tn_val_b6x5 ...
  python -m eval.b2_compare pair _tn_base _tn_b6x5

grid: a design's rule on val runs (docs/design/triplet-noise.md, reverb.md).
The first tag is the baseline; a setting qualifies when its mean accuracy
over clean and the tempo conditions is at most --margin points below the
baseline's (and, with --noise-margin, its mean over the real-noise
conditions at most that far below), and the qualifying setting chosen has
the highest mean over --target: the real-noise (demand_) or the
reverberation (room_) conditions.

pair: two runs on the same test excerpts, each at its own val-calibrated
thresholds, with the exact McNemar test per condition and the test false
accept rate of each. Writes eval/results/b2_compare_seed<seed><a>_vs<b>.{md,json}.
"""

import argparse
import json
from pathlib import Path

import numpy as np
import pandas as pd
from scipy.stats import binomtest

from eval.summarize import accepted

RES = Path(__file__).resolve().parent / "results"
TEMPO = ("speed_", "keylock_", "stretch_")


def _meta(tag: str, seed: int) -> dict:
    return json.loads((RES / f"b2_eval_seed{seed}{tag}.json").read_text())


def group_means(conds: dict[str, dict]) -> dict[str, float]:
    """Mean accuracy (points) over the real-noise conditions and over clean
    plus tempo."""
    real = [c["accuracy"] for k, c in conds.items() if k.startswith("demand_")]
    room = [c["accuracy"] for k, c in conds.items() if k.startswith("room_")]
    kept = [c["accuracy"] for k, c in conds.items() if k.startswith(("clean_", *TEMPO))]
    return {"real": _mean(real), "room": _mean(room), "kept": _mean(kept)}


def _mean(x: list[float]) -> float:
    return 100 * float(np.mean(x)) if x else float("nan")


def grid(tags: list[str], seed: int, margin: float, target: str = "real",
         noise_margin: float | None = None) -> list[dict[str, object]]:  # fmt: skip
    rows = []
    for tag in tags:
        m = _meta(tag, seed)
        rows.append({"tag": tag, "params": m["b2_params"], "split": m["reported_split"],
                     **group_means(m["systems"]["b2"]["conditions"])})  # fmt: skip
    base = rows[0]
    for r in rows:
        r["qualifies"] = r["kept"] >= base["kept"] - margin and (
            noise_margin is None or r["real"] >= base["real"] - noise_margin
        )
    best = max((r for r in rows[1:] if r["qualifies"]), key=lambda r: r[target], default=None)
    for r in rows:
        r["chosen"] = r is best
    return rows


def ok_test(tag: str, seed: int) -> tuple[pd.Series, float, dict]:
    """Per test positive (fma_id, cond): accepted and correct, at the run's
    thresholds; and the run's test FAR."""
    d = pd.read_csv(RES / f"b2_eval_seed{seed}{tag}.csv.gz")
    d = d[(d["split"] == "test") & (d["system"] == "b2")]
    th = _meta(tag, seed)["systems"]["b2"]
    acc = accepted(d, th["t_abs"], th["t_ratio"])
    pos = (d["label"] == "pos").to_numpy()
    ok = pd.Series(
        acc[pos] & d["correct"].to_numpy()[pos],
        index=pd.MultiIndex.from_frame(d.loc[pos, ["fma_id", "cond"]]),
    )
    return ok, float(acc[~pos].mean()), {k: th[k] for k in ("t_abs", "t_ratio")}


def pair(a_tag: str, b_tag: str, seed: int) -> dict[str, object]:
    a, far_a, th_a = ok_test(a_tag, seed)
    b, far_b, th_b = ok_test(b_tag, seed)
    a, b = a.align(b, join="inner")
    cells: dict[str, dict[str, object]] = {}
    for cond in a.index.get_level_values("cond").unique():
        x, y = a.xs(cond, level="cond"), b.xs(cond, level="cond")
        only_b, only_a = int((~x & y).sum()), int((x & ~y).sum())
        n = only_a + only_b
        cells[cond] = {"n": len(x), "accuracy_a": float(x.mean()), "accuracy_b": float(y.mean()),
                       "only_b": only_b, "only_a": only_a,
                       "p": binomtest(only_b, n, 0.5).pvalue if n else 1.0}  # fmt: skip
    means = {
        side: group_means({k: {"accuracy": c[f"accuracy_{side}"]} for k, c in cells.items()})
        for side in ("a", "b")
    }
    return {"a": a_tag, "b": b_tag, "seed": seed, "thresholds": {"a": th_a, "b": th_b},
            "test_far": {"a": far_a, "b": far_b}, "means": means,
            "accuracy": cells}  # fmt: skip


def markdown_pair(r: dict[str, object]) -> str:
    th, far, means = r["thresholds"], r["test_far"], r["means"]
    out = [f"# Triplets {r['b']} vs {r['a']}, test split, exact McNemar", "",
           "Each run at its own val-calibrated thresholds; the same test excerpts.", "",
           "| Condition | n | A | B | right only with B | right only with A | p |",
           "|---|---|---|---|---|---|---|"]  # fmt: skip
    for cond, c in r["accuracy"].items():
        out.append(f"| {cond} | {c['n']} | {100 * c['accuracy_a']:.1f} | "
                   f"{100 * c['accuracy_b']:.1f} | {c['only_b']} | {c['only_a']} | "
                   f"{c['p']:.3g} |")  # fmt: skip
    out += [""]
    for side, tag in (("a", r["a"]), ("b", r["b"])):
        out.append(f"{side.upper()} ({tag}): t_abs {th[side]['t_abs']}, t_ratio "
                   f"{th[side]['t_ratio']}, test FAR {100 * far[side]:.2f}%, mean real noise "
                   f"{means[side]['real']:.2f}, mean clean and tempo "
                   f"{means[side]['kept']:.2f}")  # fmt: skip
    return "\n".join(out) + "\n"


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--seed", type=int, default=0)
    sub = ap.add_subparsers(dest="cmd", required=True)
    g = sub.add_parser("grid", help="the design's selection rule on val runs")
    g.add_argument("tags", nargs="+", help="baseline first")
    g.add_argument("--margin", type=float, default=0.5, help="clean and tempo, points")
    g.add_argument("--target", choices=["real", "room"], default="real",
                   help="the group to maximize: real noise (D53) or reverberation")  # fmt: skip
    g.add_argument("--noise-margin", type=float,
                   help="real noise at most this many points below the baseline too")  # fmt: skip
    p = sub.add_parser("pair", help="two runs on the same test excerpts, McNemar")
    p.add_argument("a")
    p.add_argument("b")
    args = ap.parse_args()
    if args.cmd == "grid":
        for r in grid(args.tags, args.seed, args.margin, args.target, args.noise_margin):
            print(f"{r['tag']:28s} real {r['real']:6.2f} room {r['room']:6.2f} "
                  f"kept {r['kept']:6.2f} qualifies {r['qualifies']!s:5s} "
                  f"chosen {r['chosen']!s:5s} {r['params']}")  # fmt: skip
        return
    r = pair(args.a, args.b, args.seed)
    stem = RES / f"b2_compare_seed{args.seed}{args.a}_vs{args.b}"
    Path(f"{stem}.json").write_text(json.dumps(r, indent=1) + "\n")
    Path(f"{stem}.md").write_text(markdown_pair(r))
    print(markdown_pair(r))


if __name__ == "__main__":
    main()
