"""Run the demo web page on this machine (cmd/demo).

  make demo                  # or: python -m bench.scripts.demo --stations 3

Starts the triplet and pair clusters (stop list 4%, as the hybrid was
calibrated), then, unless --stations 0, live monitoring: a registry, a
stream server looping UnmixDB test mixes as stations, and one worker.
Then bin/demo on http://127.0.0.1:8080 until Ctrl-C. Logs go to a
temporary directory, printed at start.
"""

import argparse
import json
import random
import signal
import subprocess
import tempfile
import time
from pathlib import Path

from bench.scripts.benchcluster import BIN, ROOT, cluster, wait_for
from bench.scripts.live import B2_INDEX, PAIR_INDEX, TEST_SETS, mixes


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--stations", type=int, default=3, help="live test stations (0: no live tab)")
    ap.add_argument("--per-station", type=int, default=4, help="mixes per station, looped")
    ap.add_argument("--listen", default="127.0.0.1:8080")
    ap.add_argument("--seed", type=int, default=2)
    args = ap.parse_args()
    need = ["shard", "coordinator", "demo"]
    if args.stations:
        need += ["registry", "monitor", "streamserver"]
    for b in need:
        if not (BIN / b).exists():
            raise SystemExit(f"bin/{b} missing: run `make build`")
    tmp = Path(tempfile.mkdtemp(prefix="landmark-demo-"))
    print(f"logs in {tmp}; starting the clusters (about a minute)...", flush=True)
    procs: list[subprocess.Popen] = []

    def start(name: str, cmd: list[str], ready: str | None = "serving") -> None:
        log = tmp / f"{name}.log"
        procs.append(subprocess.Popen(cmd, stdout=log.open("w"), stderr=subprocess.STDOUT))
        if ready:
            wait_for(log, ready, 90)

    with (
        cluster(B2_INDEX, 1, 10000, 8, base_port=7100, stop_fraction=0.04) as (b2, _),
        cluster(PAIR_INDEX, 1, 10000, 8, base_port=7200, stop_fraction=0.04) as (pairs, _),
    ):
        try:
            demo = [str(BIN / "demo"), "-listen", args.listen, "-coordinator", b2,
                    "-index", str(B2_INDEX), "-pairs-coordinator", pairs,
                    "-pairs-index", str(PAIR_INDEX)]  # fmt: skip
            if args.stations:
                pool = mixes("unmixdb_s8", TEST_SETS)
                random.Random(args.seed).shuffle(pool)
                n = args.per_station
                stations = [{"id": f"st{i + 1:02d}",
                             "files": [str(ROOT / pool[i * n + j]) for j in range(n)]}
                            for i in range(args.stations)]  # fmt: skip
                (tmp / "stations.json").write_text(json.dumps(stations))
                streams = [{"id": s["id"], "url": f"http://127.0.0.1:7950/stream/{s['id']}"}
                           for s in stations]  # fmt: skip
                (tmp / "streams.json").write_text(json.dumps(streams))
                start("registry", [str(BIN / "registry"), "-dir", str(tmp / "registry"),
                                   "-streams", str(tmp / "streams.json"),
                                   "-listen", "127.0.0.1:7900", "-admin", "127.0.0.1:7901",
                                   "-ttl", "6s"])  # fmt: skip
                start("streamserver", [str(BIN / "streamserver"), "-stations",
                                       str(tmp / "stations.json"), "-listen", "127.0.0.1:7950",
                                       "-loop", "-start", f"{time.time() + 8:.6f}"],
                      "broadcasting")  # fmt: skip
                start("monitor", [str(BIN / "monitor"), "-registry", "127.0.0.1:7900",
                                  "-id", "worker-1", "-capacity", str(args.stations),
                                  "-coordinator", b2, "-index", str(B2_INDEX),
                                  "-pairs-coordinator", pairs, "-pairs-index", str(PAIR_INDEX),
                                  "-admin", "127.0.0.1:7910"], None)  # fmt: skip
                demo += ["-registry", "127.0.0.1:7900"]
            start("demo", demo)
            print(f"\nLandmark demo: http://{args.listen}  (Ctrl-C to stop)\n", flush=True)
            procs[-1].wait()
        except KeyboardInterrupt:
            pass
        finally:
            for p in procs:
                if p.poll() is None:
                    p.send_signal(signal.SIGTERM)
            for p in procs:
                try:
                    p.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    p.kill()


if __name__ == "__main__":
    main()
