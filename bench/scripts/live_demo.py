"""Watch live monitoring work, on this machine (D38-D40).

  python -m bench.scripts.live_demo --stations 3

Starts the triplet and pair clusters (stop list 4%), a registry, a stream
server playing UnmixDB test mixes as live stations, and one monitor worker,
then prints each song as it is recognized (bin/events) until Ctrl-C. Each
line is a decision made from audio as it arrives: a song is reported about
12.5 s after it fades in. Nothing is measured or saved; for measurements
use bench.scripts.live.
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
    ap.add_argument("--stations", type=int, default=3)
    ap.add_argument("--per-station", type=int, default=3, help="mixes per station, back to back")
    ap.add_argument("--seed", type=int, default=1)
    args = ap.parse_args()
    for b in ("shard", "coordinator", "registry", "monitor", "streamserver", "events"):
        if not (BIN / b).exists():
            raise SystemExit(f"bin/{b} missing: run `go build -o bin/ ./cmd/...`")
    pool = mixes("unmixdb_s8", TEST_SETS)
    random.Random(args.seed).shuffle(pool)
    n = args.per_station
    stations = [{"id": f"st{i + 1:02d}", "files": [str(ROOT / pool[i * n + j]) for j in range(n)]}
                for i in range(args.stations)]  # fmt: skip
    tmp = Path(tempfile.mkdtemp(prefix="live-demo-"))
    (tmp / "stations.json").write_text(json.dumps(stations))
    (tmp / "streams.json").write_text(
        json.dumps(
            [{"id": s["id"], "url": f"http://127.0.0.1:7950/stream/{s['id']}"} for s in stations]
        )
    )
    print(f"logs in {tmp}; starting the clusters (about a minute)...", flush=True)
    procs: list[subprocess.Popen] = []

    def start(name: str, cmd: list[str], ready: str | None = "serving") -> None:
        log = tmp / f"{name}.log"
        procs.append(subprocess.Popen(cmd, stdout=log.open("w"), stderr=subprocess.STDOUT))
        if ready:
            wait_for(log, ready, 60)

    with (
        cluster(B2_INDEX, 1, 10000, 8, base_port=7100, stop_fraction=0.04) as (b2, _),
        cluster(PAIR_INDEX, 1, 10000, 8, base_port=7200, stop_fraction=0.04) as (pairs, _),
    ):
        try:
            start("registry", [str(BIN / "registry"), "-dir", str(tmp / "registry"), "-streams",
                               str(tmp / "streams.json"), "-listen", "127.0.0.1:7900",
                               "-admin", "127.0.0.1:7901", "-ttl", "6s"])  # fmt: skip
            on_air = f"{time.time() + 10:.6f}"
            start("streamserver", [str(BIN / "streamserver"), "-stations",
                                   str(tmp / "stations.json"), "-listen", "127.0.0.1:7950",
                                   "-start", on_air], "broadcasting")  # fmt: skip
            start("monitor", [str(BIN / "monitor"), "-registry", "127.0.0.1:7900", "-id", "w1",
                              "-capacity", str(args.stations), "-coordinator", b2,
                              "-index", str(B2_INDEX), "-pairs-coordinator", pairs,
                              "-pairs-index", str(PAIR_INDEX), "-admin", "127.0.0.1:7910"],
                  None)  # fmt: skip
            print("stations on the air; songs appear about 12 s after they start"
                  " (Ctrl-C to stop)\n", flush=True)  # fmt: skip
            events = subprocess.Popen([str(BIN / "events"), "-registry", "127.0.0.1:7900"])
            procs.append(events)
            events.wait()
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
