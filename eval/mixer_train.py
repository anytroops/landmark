"""Train and test the mixer-moves model on eval.mixsim's rendered mixes
(docs/design/mixer-moves.md).

  python -m eval.mixer_train features --split train      # and val, test
  python -m eval.mixer_train train --lr 1e-3 --tag a     # selection on val
  python -m eval.mixer_train test --tag a                # once

features: for every song of every mix, its features and labels, with the
alignment jittered (up to 2 frames, 0.1% of rate; seeded per mix) as a real
pipeline's estimate would be, and B1 fitted on the same warped songs:
data/mixsim/<split>_features.npz. train: the network on the train split,
checkpoints scored on val (mean absolute error in dB over playing frames),
the best kept in data/mixsim/model_<tag>.pt. test: B0, B1, and the model on
the test split (gains, and bass-swap timing), written to
eval/results/mixer/test_<tag>.json.
"""

import argparse
import json
import multiprocessing as mp
import time
from pathlib import Path

import numpy as np
from scipy.ndimage import median_filter, uniform_filter1d

from eval.dj_analysis import FPS, gains
from prototype import b2, mixer
from prototype.audio import decode
from prototype.fma import audio_path

ROOT = Path(__file__).resolve().parent.parent
DATA = ROOT / "data" / "mixsim"
RES = ROOT / "eval" / "results" / "mixer"
JITTER_FRAMES, JITTER_RATE = 2.0, 0.001
CHUNK = 512


def ref_power(track: int) -> np.ndarray:
    cache = DATA / "refs" / f"{track}.npy"
    if cache.exists():
        db = np.load(cache)
    else:
        db = b2.spectrogram(decode(audio_path(track, "medium"))).astype(np.float16)
        cache.parent.mkdir(parents=True, exist_ok=True)
        np.save(cache, db)
    return 10.0 ** (db.astype(np.float64) / 10.0)


def load_mix(path: Path) -> tuple[np.ndarray, list[dict], list[dict]]:
    z = np.load(path)
    songs = [{"track": int(z["tracks"][i]), "mode": str(z["modes"][i]),
              "first": int(z["first"][i]), "last": int(z["last"][i]), "a": float(z["a"][i]),
              "rate": float(z["rate"][i]), "gains": z[f"gains{i}"].astype(np.float64)}
             for i in range(len(z["tracks"]))]  # fmt: skip
    return 10.0 ** (z["spec"].astype(np.float64) / 10.0), songs, json.loads(str(z["moves"]))


def prepare(path: Path, k: int, jitter: bool = True) -> dict:
    """Features, labels, B0, and B1 for every song of one mix."""
    power, songs, moves = load_mix(path)
    rng = np.random.default_rng([7, k])
    model_songs = []
    for s in songs:
        da = rng.uniform(-JITTER_FRAMES, JITTER_FRAMES) / FPS if jitter else 0.0
        dr = 1.0 + (rng.uniform(-JITTER_RATE, JITTER_RATE) if jitter else 0.0)
        model_songs.append(mixer.Song(ref_power(s["track"]), s["a"] + da, s["rate"] * dr,
                                      s["mode"] == "speed", s["first"], s["last"]))  # fmt: skip
    sources = mixer.warped(model_songs, len(power))
    b1 = mixer.fit_b1(power, sources)
    b0 = broadband_b0(power, sources)
    out = []
    for i, s in enumerate(songs):
        sl = slice(s["first"], s["last"] + 1)
        out.append({"x": mixer.features(power, sources, b1, i)[sl].astype(np.float16),
                    "y": mixer.to_db(s["gains"]).astype(np.float32),
                    "b1": mixer.to_db(b1[i, sl]).astype(np.float32),
                    "b0": mixer.to_db(b0[i, sl]).astype(np.float32),
                    "first": s["first"]})  # fmt: skip
    return {"songs": out, "moves": moves}


def broadband_b0(power: np.ndarray, sources: list[np.ndarray]) -> np.ndarray:
    """D43's broadband gain per song, the same in all three bands."""
    active = np.stack([s.sum(axis=1) > 0 for s in sources], axis=1)
    g = gains(power, sources, active)
    out = np.zeros((len(sources), len(power), 3))
    for k in range(len(sources)):
        x = uniform_filter1d(median_filter(g[:, k], size=mixer.MEDIAN_FRAMES, mode="nearest"),
                             mixer.AVERAGE_FRAMES, mode="nearest")  # fmt: skip
        x[~active[:, k]] = 0.0
        out[k] = x[:, None]
    return out


def _prepare(job: tuple[Path, int]) -> dict:
    return prepare(*job)


def features_cmd(split: str, workers: int) -> None:
    paths = sorted((DATA / split).glob("*.npz"))
    with mp.get_context("fork").Pool(workers) as p:
        mixes = p.map(_prepare, [(q, k) for k, q in enumerate(paths)], chunksize=2)
    songs = [s for m in mixes for s in m["songs"]]
    lengths = np.array([len(s["y"]) for s in songs])
    np.savez(DATA / f"{split}_features.npz", x=np.concatenate([s["x"] for s in songs]),
             y=np.concatenate([s["y"] for s in songs]), b1=np.concatenate([s["b1"] for s in songs]),
             b0=np.concatenate([s["b0"] for s in songs]), lengths=lengths,
             mix=np.repeat(np.arange(len(mixes)), [len(m["songs"]) for m in mixes]),
             first=np.array([s["first"] for s in songs]),
             moves=json.dumps([m["moves"] for m in mixes]))  # fmt: skip
    print(f"{split}: {len(mixes)} mixes, {len(songs)} songs, {lengths.sum()} frames")


def load_features(split: str) -> dict:
    z = np.load(DATA / f"{split}_features.npz")
    d = {k: z[k] for k in ("x", "y", "b1", "b0", "lengths", "mix", "first")}
    d["offsets"] = np.concatenate([[0], np.cumsum(d["lengths"])])
    d["moves"] = json.loads(str(z["moves"]))
    return d


def predict(model, d: dict, device: str) -> np.ndarray:
    """The model's dB gains for every frame of a split, song by song."""
    import torch

    out = np.zeros_like(d["y"])
    model.eval()
    with torch.no_grad():
        for i in range(len(d["lengths"])):
            a, b = d["offsets"][i], d["offsets"][i + 1]
            x = torch.from_numpy(d["x"][a:b].astype(np.float32).T[None]).to(device)
            out[a:b] = (model(x)[0].T.cpu().numpy() * 20.0).astype(np.float32)
    return out


def mae(pred: np.ndarray, y: np.ndarray) -> list[float]:
    return np.abs(np.maximum(pred, mixer.FLOOR_DB) - y).mean(axis=0).round(4).tolist()


def train_cmd(args: argparse.Namespace) -> None:
    import torch

    from prototype.mixer_model import MixerTCN

    torch.manual_seed(args.seed)
    rng = np.random.default_rng(args.seed)
    device = "mps" if torch.backends.mps.is_available() else "cpu"
    tr, va = load_features("train"), load_features("val")
    model = MixerTCN(args.channels, args.layers).to(device)
    opt = torch.optim.AdamW(model.parameters(), lr=args.lr, weight_decay=1e-4)
    sched = torch.optim.lr_scheduler.OneCycleLR(opt, max_lr=args.lr, total_steps=args.steps)
    starts_ok = [(i, max(int(n) - CHUNK, 0)) for i, n in enumerate(tr["lengths"])]
    best, log, t0 = None, [], time.time()
    for step in range(1, args.steps + 1):
        model.train()
        xs, ys, ms = [], [], []
        for i in rng.integers(0, len(starts_ok), args.batch):
            song, top = starts_ok[i]
            s0 = tr["offsets"][song] + (int(rng.integers(0, top + 1)) if top else 0)
            n = min(CHUNK, int(tr["lengths"][song]))
            x = np.zeros((CHUNK, tr["x"].shape[1]), np.float32)
            y = np.zeros((CHUNK, 3), np.float32)
            m = np.zeros(CHUNK, np.float32)
            x[:n], y[:n], m[:n] = tr["x"][s0 : s0 + n], tr["y"][s0 : s0 + n], 1.0
            xs.append(x.T)
            ys.append(y.T)
            ms.append(m)
        x = torch.from_numpy(np.stack(xs)).to(device)
        y = torch.from_numpy(np.stack(ys)).to(device) / 20.0
        m = torch.from_numpy(np.stack(ms)).to(device)[:, None]
        loss = ((model(x) - y).abs() * m).sum() / (m.sum() * 3)
        opt.zero_grad()
        loss.backward()
        torch.nn.utils.clip_grad_norm_(model.parameters(), 1.0)
        opt.step()
        sched.step()
        if step % args.eval_every == 0 or step == args.steps:
            v = mae(predict(model, va, device), va["y"])
            log.append({"step": step, "train_loss_db": loss.item() * 20, "val_mae_db": v,
                        "seconds": round(time.time() - t0)})  # fmt: skip
            print(json.dumps(log[-1]), flush=True)
            if best is None or np.mean(v) < best[0]:
                best = (float(np.mean(v)), step)
                torch.save(model.state_dict(), DATA / f"model_{args.tag}.pt")
    RES.mkdir(parents=True, exist_ok=True)
    meta = {"tag": args.tag, "config": vars(args), "best_val_mae_db": best[0], "best_step": best[1],
            "val_b1_mae_db": mae(va["b1"], va["y"]), "val_b0_mae_db": mae(va["b0"], va["y"]),
            "log": log}  # fmt: skip
    (RES / f"train_{args.tag}.json").write_text(json.dumps(meta, indent=1) + "\n")


def transitions(d: dict) -> np.ndarray:
    """Per frame of a split's songs, whether it lies inside a transition."""
    inside = np.zeros(len(d["y"]), bool)
    hz = 100.0  # eval.mixsim.CONTROL_HZ
    for i in range(len(d["lengths"])):
        a, b, first = d["offsets"][i], d["offsets"][i + 1], int(d["first"][i])
        frames = np.arange(first, first + b - a)
        for m in d["moves"][int(d["mix"][i])]:
            lo, hi = m["start"] / hz * FPS, m["end"] / hz * FPS
            inside[a:b] |= (frames >= lo) & (frames <= hi)
    return inside


def crossing(d: np.ndarray) -> int:
    """The frame where d (incoming minus outgoing low-band gain, dB) turns
    from negative to positive: the split that leaves the fewest frames on
    the wrong side."""
    neg = np.concatenate([[0], np.cumsum(d < 0)])
    pos = np.concatenate([np.cumsum((d >= 0)[::-1])[::-1], [0]])
    return int(np.argmax(neg + pos))


def swap_detection(d: dict, pred: np.ndarray) -> dict:
    """Bass swaps (docs/design/mixer-moves.md): the frame where the low band
    passes from the outgoing song to the incoming one, read from the
    predicted gains with crossing(), against the same frame in the labels."""
    errs = []
    for m, moves in enumerate(d["moves"]):
        songs = np.nonzero(d["mix"] == m)[0]
        for j, mv in enumerate(moves):
            if mv["move"] != "bass_swap" or j + 1 >= len(songs):
                continue
            out_s, in_s = songs[j], songs[j + 1]
            lo = max(int(d["first"][out_s]), int(d["first"][in_s]))
            hi = min(int(d["first"][out_s]) + int(d["lengths"][out_s]),
                     int(d["first"][in_s]) + int(d["lengths"][in_s]))  # fmt: skip
            if hi - lo < 2:
                continue

            def low(x: np.ndarray, s: int, lo: int = lo, n: int = hi - lo) -> np.ndarray:
                a = d["offsets"][s] + lo - int(d["first"][s])
                return np.maximum(x[a : a + n, 0], mixer.FLOOR_DB)

            true = crossing(low(d["y"], in_s) - low(d["y"], out_s))
            est = crossing(low(pred, in_s) - low(pred, out_s))
            errs.append(abs(est - true) / FPS)
    e = np.array(errs)
    return {"swaps": len(e), "within_1s": round(float(np.mean(e <= 1.0)), 4),
            "median_err_s": round(float(np.median(e)), 3)}  # fmt: skip


def load_model(tag: str, device: str = "cpu"):
    """A trained model by its tag (data/mixsim/model_<tag>.pt)."""
    import torch

    from prototype.mixer_model import MixerTCN

    cfg = json.loads((RES / f"train_{tag}.json").read_text())["config"]
    model = MixerTCN(cfg["channels"], cfg["layers"]).to(device)
    model.load_state_dict(torch.load(DATA / f"model_{tag}.pt", map_location=device))
    return model.eval()


def test_cmd(args: argparse.Namespace) -> None:
    import torch

    device = "mps" if torch.backends.mps.is_available() else "cpu"
    model = load_model(args.tag, device)
    te = load_features("test")
    pred = predict(model, te, device)
    inside = transitions(te)
    result = {"tag": args.tag, "frames": int(len(te["y"])), "transition_frames": int(inside.sum())}
    for name, p in (("b0", te["b0"]), ("b1", te["b1"]), ("model", pred)):
        result[name] = {
            "mae_db": mae(p, te["y"]),
            "transitions_mae_db": mae(p[inside], te["y"][inside]),
            "bass_swaps": swap_detection(te, p),
        }
    (RES / f"test_{args.tag}.json").write_text(json.dumps(result, indent=1) + "\n")
    print(json.dumps(result, indent=1))


def main() -> None:
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    f = sub.add_parser("features")
    f.add_argument("--split", choices=["train", "val", "test"], required=True)
    f.add_argument("--workers", type=int, default=10)
    t = sub.add_parser("train")
    t.add_argument("--tag", required=True)
    t.add_argument("--lr", type=float, default=1e-3)
    t.add_argument("--channels", type=int, default=64)
    t.add_argument("--layers", type=int, default=6)
    t.add_argument("--batch", type=int, default=32)
    t.add_argument("--steps", type=int, default=8000)
    t.add_argument("--eval-every", type=int, default=1000)
    t.add_argument("--seed", type=int, default=0)
    s = sub.add_parser("test")
    s.add_argument("--tag", required=True)
    args = ap.parse_args()
    if args.cmd == "features":
        features_cmd(args.split, args.workers)
    elif args.cmd == "train":
        train_cmd(args)
    else:
        test_cmd(args)


if __name__ == "__main__":
    main()
