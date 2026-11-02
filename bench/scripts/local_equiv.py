"""tracklist -local against a cluster's offline runs, mix by mix (D54).

  python -m bench.scripts.local_equiv --runs eval/results/live/equiv_test_d53/offline_runs.jsonl

Runs bin/tracklist -local (the indexes served in process, internal/local)
on every mix of --runs, a cluster's offline output from
`python -m bench.scripts.live equiv`, with the same flags, and counts the
mixes whose entries differ in any field the cluster run has (fields added
since, such as source positions, are not compared). Writes
eval/results/local/equiv<tag>.json.
"""

import argparse
import concurrent.futures
import json
import subprocess
import time
from pathlib import Path

from bench.scripts.benchcluster import BIN, ROOT
from bench.scripts.live import B2_INDEX, PAIR_INDEX, git_head


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--runs", type=Path, required=True)
    ap.add_argument("--timeout", default="10s", help="per-search deadline, as the cluster run had")
    ap.add_argument("--parallel", type=int, default=2)
    ap.add_argument("--tag", default="")
    args = ap.parse_args()
    ref = [json.loads(line) for line in args.runs.read_text().splitlines()]

    def one(r: dict) -> tuple[str, bool]:
        out = subprocess.run(
            [str(BIN / "tracklist"), "-local", "-index", str(B2_INDEX),
             "-pairs-index", str(PAIR_INDEX), "-timeout", args.timeout, "-json",
             str(ROOT / r["file"])],
            capture_output=True, text=True, check=True,
        ).stdout  # fmt: skip
        got, want = json.loads(out), r["entries"]
        same = len(got) == len(want) and all(
            all(g.get(k) == v for k, v in w.items()) for g, w in zip(got, want, strict=True)
        )
        return r["file"], same

    t0 = time.time()
    with concurrent.futures.ThreadPoolExecutor(args.parallel) as ex:
        diff = [f for f, same in ex.map(one, ref) if not same]
    result = {"runs": str(args.runs), "mixes": len(ref), "differing": len(diff), "files": diff,
              **git_head(), "wall_s": round(time.time() - t0, 1)}  # fmt: skip
    out = ROOT / "eval/results/local" / f"equiv{args.tag}.json"
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(result, indent=1) + "\n")
    print(json.dumps(result))


if __name__ == "__main__":
    main()
