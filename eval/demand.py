"""Real background noise for query degradations: DEMAND (Thiemann, Ito, and
Vincent, 2013; zenodo.org/records/1227121, CC BY 4.0), recordings of 18
everyday environments made with a 16-microphone array.

We use channel 1 of the 16 kHz release for ten environments where people
identify music (eating out, stations, streets, vehicles, home, a park),
resampled to the fingerprint's 11,025 Hz. Each recording is 5 minutes; val
queries draw their noise from the first half and test queries from the
second, so a noise window used for tuning never reaches test.

Prepare once (reads the downloaded zips, writes data/noise/demand/):
    python -m eval.demand prepare --zips data/noise_download
"""

import argparse
import functools
import hashlib
import io
import itertools
import json
import zipfile
from pathlib import Path

import numpy as np
import pandas as pd
from scipy.io import wavfile
from scipy.signal import resample_poly
from scipy.stats import binomtest

from eval.compare import accepted_correct
from eval.summarize import accepted
from prototype.audio import SAMPLE_RATE, decode
from prototype.fma import DATA_DIR, audio_path

ROOT = Path(__file__).resolve().parent.parent
NOISE_DIR = DATA_DIR / "noise" / "demand"
SOURCE_RATE = 16_000
SNRS = (5, 0, -5, -10)  # the report's columns, dB
BAND_EDGES_HZ = [55, 300, 2000]  # the triplet filterbank's floor; bass; the speech band
# Condition names use these in lower case: demand_pcafeter_snr0_10s.
ENVIRONMENTS = (
    "PCAFETER",  # cafeteria: crowd chatter, dishes
    "PRESTO",  # restaurant
    "PSTATION",  # train station concourse
    "STRAFFIC",  # busy street
    "SPSQUARE",  # public square
    "TBUS",  # inside a bus
    "TCAR",  # inside a car
    "TMETRO",  # inside a subway train
    "DLIVING",  # living room
    "NPARK",  # city park
)


def prepare(zips: Path, out: Path) -> dict[str, dict[str, object]]:
    """Writes {ENV}.npy (float32 at SAMPLE_RATE) and manifest.json from the
    zips' channel 1, read from the archive in memory (nothing extracted)."""
    out.mkdir(parents=True, exist_ok=True)
    manifest: dict[str, dict[str, object]] = {}
    for env in ENVIRONMENTS:
        path = zips / f"{env}_16k.zip"
        with zipfile.ZipFile(path) as z:
            members = [n for n in z.namelist() if n.endswith("ch01.wav")]
            if len(members) != 1:
                raise ValueError(f"{path}: expected one ch01.wav, found {members}")
            rate, x = wavfile.read(io.BytesIO(z.read(members[0])))
        if rate != SOURCE_RATE or x.ndim != 1:
            raise ValueError(f"{path}: {rate} Hz, shape {x.shape}; want mono {SOURCE_RATE} Hz")
        if np.issubdtype(x.dtype, np.integer):
            x = x / float(np.iinfo(x.dtype).max + 1)
        y = resample_poly(x.astype(np.float64), SAMPLE_RATE, SOURCE_RATE).astype(np.float32)
        np.save(out / f"{env}.npy", y)
        manifest[env] = {
            "zip": path.name,
            "zip_md5": hashlib.md5(path.read_bytes()).hexdigest(),
            "member": members[0],
            "seconds": round(len(y) / SAMPLE_RATE, 2),
            "rms": float(np.sqrt(np.mean(y.astype(np.float64) ** 2))),
        }
    (out / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    return manifest


@functools.cache
def recording(env: str) -> np.ndarray:
    """One environment's prepared recording (cached per process)."""
    if env not in ENVIRONMENTS:
        raise KeyError(f"unknown DEMAND environment {env!r}")
    path = NOISE_DIR / f"{env}.npy"
    if not path.exists():
        raise FileNotFoundError(f"{path} missing: run python -m eval.demand prepare")
    return np.load(path)


def noise(env: str, split: str) -> np.ndarray:
    """The half of an environment's recording that a split's queries draw
    from: the first for val, the second for test."""
    x = recording(env)
    mid = len(x) // 2
    if split == "val":
        return x[:mid]
    if split == "test":
        return x[mid:]
    raise ValueError(f"unknown split {split!r}")


def band_shares(x: np.ndarray) -> list[float]:
    """Percent of x's power in each band between BAND_EDGES_HZ (and below and above)."""
    power = np.abs(np.fft.rfft(x.astype(np.float64))) ** 2
    freq = np.fft.rfftfreq(len(x), 1 / SAMPLE_RATE)
    edges = [0.0, *BAND_EDGES_HZ, SAMPLE_RATE / 2 + 1]
    total = float(power.sum())
    return [
        round(100 * float(power[(freq >= lo) & (freq < hi)].sum()) / total, 1)
        for lo, hi in itertools.pairwise(edges)
    ]


def report(tag: str, seed: int, manifest: Path, music_tracks: int) -> dict[str, object]:
    """Each environment's accuracy at threshold next to white noise at the
    same SNR on the same excerpts (exact McNemar), and where each noise's
    power lies against music's."""
    ok = accepted_correct(tag, seed)
    accuracy: dict[str, dict[str, object]] = {}
    for env in ("white", *(e.lower() for e in ENVIRONMENTS)):
        for db in SNRS:
            cond = f"white_snr{db}_10s" if env == "white" else f"demand_{env}_snr{db}_10s"
            y = ok.xs(cond, level="cond")
            cell: dict[str, object] = {"accuracy": float(y.mean()), "n": len(y)}
            if env != "white":
                x = ok.xs(f"white_snr{db}_10s", level="cond").reindex(y.index)
                only, white_only = int((~x & y).sum()), int((x & ~y).sum())
                n = only + white_only
                cell |= {"only_here": only, "only_white": white_only,
                         "p": binomtest(only, n, 0.5).pvalue if n else 1.0}  # fmt: skip
            accuracy[cond] = cell
    rng = np.random.default_rng(seed)
    bands = {env.lower(): band_shares(noise(env, "test")) for env in ENVIRONMENTS}
    bands["white"] = band_shares(rng.standard_normal(150 * SAMPLE_RATE))
    m = json.loads(manifest.read_text())
    music = [band_shares(decode(audio_path(f, m["meta"]["subset"])))
             for f in m["test_pos"][:music_tracks]]  # fmt: skip
    bands[f"music, mean of {len(music)} test tracks"] = [
        round(float(v), 1) for v in np.mean(music, axis=0)
    ]
    return {"tag": tag, "seed": seed, "band_edges_hz": BAND_EDGES_HZ, "bands": bands,
            "accuracy": accuracy}  # fmt: skip


def markdown(r: dict[str, object]) -> str:
    acc = r["accuracy"]
    head = " | ".join(f"{db} dB" for db in SNRS)
    out = [f"# Real background noise, test accuracy at threshold ({r['tag']})", "",
           "Each cell: accuracy, then queries right only here / only with white noise at the "
           "same SNR, and the exact McNemar p.", "",
           f"| Noise | {head} |", "|---|" + "---|" * len(SNRS)]  # fmt: skip
    for env in ("white", *(e.lower() for e in ENVIRONMENTS)):
        cells = []
        for db in SNRS:
            c = acc[f"white_snr{db}_10s" if env == "white" else f"demand_{env}_snr{db}_10s"]
            s = f"{100 * c['accuracy']:.1f}%"
            if env != "white":
                s += f" ({c['only_here']}/{c['only_white']}, p {c['p']:.2g})"
            cells.append(s)
        out.append(f"| {env} | " + " | ".join(cells) + " |")
    out += ["", *band_table(r["band_edges_hz"], r["bands"])]
    return "\n".join(out) + "\n"


def band_table(edges: list[int], bands: dict[str, list[float]]) -> list[str]:
    heads = [f"below {edges[0]} Hz"]
    heads += [f"{lo} to {hi} Hz" for lo, hi in itertools.pairwise(edges)]
    heads += [f"above {edges[-1]} Hz"]
    out = ["| Power share | " + " | ".join(heads) + " |", "|---|" + "---|" * len(heads)]
    return out + [f"| {k} | " + " | ".join(f"{v}%" for v in s) + " |" for k, s in bands.items()]


def report_b2(tag: str, seed: int) -> dict[str, object]:
    """From an eval.b2_eval run: pairs and triplets (B2) on the same excerpts
    and catalog, each at its own thresholds, with the exact McNemar test
    between them per condition."""
    res = ROOT / "eval" / "results"
    d = pd.read_csv(res / f"b2_eval_seed{seed}{tag}.csv.gz")
    d = d[(d["split"] == "test") & (d["label"] == "pos")]
    meta = json.loads((res / f"b2_eval_seed{seed}{tag}.json").read_text())
    ok = {}
    for system in ("pairs", "b2"):
        th = meta["systems"][system]
        g = d[d["system"] == system]
        ok[system] = pd.Series(
            accepted(g, th["t_abs"], th["t_ratio"]) & g["correct"].to_numpy(),
            index=pd.MultiIndex.from_frame(g[["fma_id", "cond"]]),
        )
    pairs, trip = ok["pairs"].align(ok["b2"], join="inner")
    cells: dict[str, dict[str, object]] = {}
    for cond in pairs.index.get_level_values("cond").unique():
        x, y = pairs.xs(cond, level="cond"), trip.xs(cond, level="cond")
        only_b2, only_pairs = int((~x & y).sum()), int((x & ~y).sum())
        n = only_b2 + only_pairs
        cells[cond] = {"n": len(x), "pairs": float(x.mean()), "b2": float(y.mean()),
                       "only_b2": only_b2, "only_pairs": only_pairs,
                       "p": binomtest(only_b2, n, 0.5).pvalue if n else 1.0}  # fmt: skip
    return {"tag": tag, "seed": seed, "catalog_tracks": meta["catalog_tracks"],
            "thresholds": {s: {k: meta["systems"][s][k] for k in ("t_abs", "t_ratio")}
                           for s in ("pairs", "b2")},
            "test_far": {s: meta["systems"][s]["test_far"] for s in ("pairs", "b2")},
            "accuracy": cells}  # fmt: skip


def markdown_b2(r: dict[str, object]) -> str:
    acc = r["accuracy"]
    head = " | ".join(f"{db} dB" for db in SNRS)
    out = [f"# Pairs and triplets (B2) under real background noise ({r['tag']})", "",
           f"One catalog of {r['catalog_tracks']} fma_medium tracks, the same test excerpts. "
           "Each cell: pairs / triplets accuracy at threshold, then queries right only with "
           "triplets / only with pairs, and the exact McNemar p.", "",
           f"| Noise | {head} |", "|---|" + "---|" * len(SNRS)]  # fmt: skip
    for env in ("white", *(e.lower() for e in ENVIRONMENTS)):
        row = []
        for db in SNRS:
            c = acc[f"white_snr{db}_10s" if env == "white" else f"demand_{env}_snr{db}_10s"]
            row.append(f"{100 * c['pairs']:.1f}% / {100 * c['b2']:.1f}% "
                       f"({c['only_b2']}/{c['only_pairs']}, p {c['p']:.2g})")  # fmt: skip
        out.append(f"| {env} | " + " | ".join(row) + " |")
    th, far = r["thresholds"], r["test_far"]
    out += [
        "",
        *(
            f"{s}: t_abs {th[s]['t_abs']}, t_ratio {th[s]['t_ratio']}, "
            f"test FAR {100 * far[s]:.2f}% (all conditions)"
            for s in ("pairs", "b2")
        ),
    ]
    return "\n".join(out) + "\n"


def main() -> None:
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("prepare", help="channel 1 of each zip, resampled, to data/noise/demand")
    p.add_argument("--zips", type=Path, required=True)
    p.add_argument("--out", type=Path, default=NOISE_DIR)
    r = sub.add_parser("report", help="accuracy per environment vs white noise; power by band")
    r.add_argument("--tag", required=True, help="an eval tag with demand and white conditions")
    r.add_argument("--seed", type=int, default=0)
    r.add_argument("--manifest", type=Path,
                   default=ROOT / "eval/manifests/splits_medium_seed0.json")  # fmt: skip
    r.add_argument("--music-tracks", type=int, default=50)
    b = sub.add_parser("report-b2", help="pairs vs triplets from an eval.b2_eval run")
    b.add_argument("--tag", required=True, help="a b2_eval tag with demand and white conditions")
    b.add_argument("--seed", type=int, default=0)
    args = ap.parse_args()
    if args.cmd == "prepare":
        for env, m in prepare(args.zips, args.out).items():
            print(env, m["seconds"], "s, rms", round(float(m["rms"]), 4))
        return
    if args.cmd == "report":
        rep, md, base = report(args.tag, args.seed, args.manifest, args.music_tracks), markdown, ""
    else:
        rep, md, base = report_b2(args.tag, args.seed), markdown_b2, "_b2"
    base = f"demand{base}_seed{args.seed}{args.tag}"  # a tag may hold a dot: _stop0.04
    res = ROOT / "eval" / "results"
    (res / f"{base}.json").write_text(json.dumps(rep, indent=1) + "\n")
    (res / f"{base}.md").write_text(md(rep))
    print(md(rep))


if __name__ == "__main__":
    main()
