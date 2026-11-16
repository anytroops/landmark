"""Learn Rekordbox-style phrases from the audio (docs/design/structure.md).

  python -m eval.structure_train data --usb ~/Desktop     # features and labels
  python -m eval.structure_train train --tag a --lr 1e-3  # choices on val
  python -m eval.structure_train test --tag a             # once

data: for the phrase-labelled songs of a Rekordbox USB export with audio on
disk, per-bar features (prototype.structure) on the grid corrected against
each song's own audio (eval.beat_align), and Rekordbox's per-bar kinds and
phrase starts; split by song (seed 0: 183 train, 40 val, the rest test) into
data/structure/songs.npz (never committed). train: R1 on the train songs,
scored on val each epoch (mean of kind accuracy and phrase-start F1), the best
kept. test: R0 and R1 on the test songs, to eval/results/structure/test_<tag>.json.
"""

import argparse
import json
import multiprocessing as mp
from pathlib import Path

import numpy as np

from eval.beat_align import best_lag, onsets
from prototype import anlz, b2, structure
from prototype.audio import DecodeError, decode

ROOT = Path(__file__).resolve().parent.parent
DATA = ROOT / "data" / "structure"
RES = ROOT / "eval" / "results" / "structure"
N_TRAIN, N_VAL = 183, 40


def _one(job: tuple[str, str, str, str]) -> dict | None:
    key, audio, dat, ext = job
    try:
        x = decode(Path(audio))
    except DecodeError:
        return None
    beats = anlz.beats(Path(dat).read_bytes())
    st = anlz.structure(Path(ext).read_bytes())
    if st is None or len(beats) < 64:
        return None
    offset = best_lag(onsets(x), np.array([b.time_s for b in beats])) / 1000.0
    power = 10.0 ** (b2.spectrogram(x).astype(np.float64) / 10.0)
    feats = structure.bar_features(power, structure.bars(beats, offset))
    kind, start = structure.labels(st, beats)
    n = min(len(feats), len(kind))
    return {"key": key, "x": feats[:n].astype(np.float32), "kind": kind[:n], "start": start[:n],
            "mood": st.mood}  # fmt: skip


def data_cmd(usb: Path, workers: int) -> None:
    lib = anlz.library(usb)
    jobs = []
    for key, (dat, ext) in sorted(lib.items()):
        audio = usb / key.lstrip("/")
        if ext.exists() and audio.exists() and anlz.structure(ext.read_bytes()) is not None:
            jobs.append((key, str(audio), str(dat), str(ext)))
    with mp.get_context("fork").Pool(workers) as p:
        songs = [s for s in p.map(_one, jobs) if s is not None]
    order = np.random.default_rng(0).permutation(len(songs))
    split = np.empty(len(songs), dtype="<U5")
    split[order[:N_TRAIN]], split[order[N_TRAIN : N_TRAIN + N_VAL]] = "train", "val"
    split[order[N_TRAIN + N_VAL :]] = "test"
    DATA.mkdir(parents=True, exist_ok=True)
    lengths = np.array([len(s["kind"]) for s in songs])
    np.savez(DATA / "songs.npz", x=np.concatenate([s["x"] for s in songs]),
             kind=np.concatenate([s["kind"] for s in songs]),
             start=np.concatenate([s["start"] for s in songs]), lengths=lengths, split=split,
             mood=np.array([s["mood"] for s in songs]))  # fmt: skip
    print(f"{len(songs)} songs, {lengths.sum()} bars; " + ", ".join(
        f"{k} {int((split == k).sum())}" for k in ("train", "val", "test")))  # fmt: skip


def load() -> list[dict]:
    z = np.load(DATA / "songs.npz")
    off = np.concatenate([[0], np.cumsum(z["lengths"])])
    return [{"x": z["x"][a:b], "kind": z["kind"][a:b], "start": z["start"][a:b],
             "split": str(z["split"][i]), "mood": str(z["mood"][i])}
            for i, (a, b) in enumerate(zip(off[:-1], off[1:], strict=True))]  # fmt: skip


def start_f1(pred: list[np.ndarray], true: list[np.ndarray]) -> dict[str, float]:
    """Phrase starts found within one bar (each true start matched once)."""
    tp = fp = fn = 0
    for p, t in zip(pred, true, strict=True):
        ps, ts = list(np.nonzero(p)[0]), set(np.nonzero(t)[0])
        for i in ps:
            hit = next((j for j in (i, i - 1, i + 1) if j in ts), None)
            if hit is None:
                fp += 1
            else:
                tp += 1
                ts.discard(hit)
        fn += len(ts)
    prec, rec = tp / max(tp + fp, 1), tp / max(tp + fn, 1)
    return {"precision": prec, "recall": rec, "f1": 2 * prec * rec / max(prec + rec, 1e-9)}


def drop_scores(pred: list[np.ndarray], true: list[np.ndarray]) -> dict[str, float]:
    p = [np.isin(np.arange(len(k)), structure.drops(k)).astype(int) for k in pred]
    t = [np.isin(np.arange(len(k)), structure.drops(k)).astype(int) for k in true]
    return start_f1(p, t)


def scores(kinds: list[np.ndarray], starts: list[np.ndarray], songs: list[dict]) -> dict:
    true_k = [s["kind"] for s in songs]
    acc = float(np.mean(np.concatenate([k == t for k, t in zip(kinds, true_k, strict=True)])))
    n = len(structure.KINDS)
    conf = np.zeros((n, n), int)  # rows: Rekordbox's kind, columns: predicted
    for k, t in zip(kinds, true_k, strict=True):
        np.add.at(conf, (t, k), 1)
    return {"kind_accuracy": acc, "starts": start_f1(starts, [s["start"] for s in songs]),
            "drops": drop_scores(kinds, true_k), "kinds": list(structure.KINDS),
            "confusion": conf.tolist()}  # fmt: skip


def run_model(model, songs: list[dict], device: str) -> tuple[list[np.ndarray], list[np.ndarray]]:
    import torch

    kinds, starts = [], []
    model.eval()
    with torch.no_grad():
        for s in songs:
            k, st = model(torch.from_numpy(s["x"][None]).to(device))
            kinds.append(k[0].argmax(-1).cpu().numpy())
            p = torch.sigmoid(st[0]).cpu().numpy()
            peaks = (p > 0.5) & (p >= np.maximum(np.roll(p, 1), np.roll(p, -1)))
            starts.append(peaks.astype(int))
    return kinds, starts


def train_cmd(args: argparse.Namespace) -> None:
    import torch

    torch.manual_seed(args.seed)
    torch.set_num_threads(2)
    device = "cpu"  # a small recurrent model, one song at a time: faster on the CPU
    songs = load()
    tr = [s for s in songs if s["split"] == "train"]
    va = [s for s in songs if s["split"] == "val"]
    counts = np.bincount(np.concatenate([s["kind"] for s in tr]), minlength=len(structure.KINDS))
    weight = torch.tensor((counts.sum() / np.maximum(counts, 1)) ** 0.5, dtype=torch.float32)
    weight = weight.to(device)
    pos = float(np.mean(np.concatenate([s["start"] for s in tr])))
    model = structure.structure_model(args.hidden).to(device)
    opt = torch.optim.AdamW(model.parameters(), lr=args.lr, weight_decay=1e-3)
    rng = np.random.default_rng(args.seed)
    best, log = None, []
    for epoch in range(1, args.epochs + 1):
        model.train()
        for i in rng.permutation(len(tr)):
            s = tr[i]
            x = torch.from_numpy(s["x"][None]).to(device)
            k, st = model(x)
            f = torch.nn.functional
            loss = f.cross_entropy(k[0], torch.from_numpy(s["kind"]).to(device), weight=weight)
            loss = loss + f.binary_cross_entropy_with_logits(
                st[0], torch.from_numpy(s["start"]).float().to(device),
                pos_weight=torch.tensor((1 - pos) / pos, device=device))  # fmt: skip
            opt.zero_grad()
            loss.backward()
            torch.nn.utils.clip_grad_norm_(model.parameters(), 1.0)
            opt.step()
        kinds, starts = run_model(model, va, device)
        v = scores(kinds, starts, va)
        score = (v["kind_accuracy"] + v["starts"]["f1"]) / 2
        log.append({"epoch": epoch, "val": v, "score": score})
        line = {"epoch": epoch, "score": score, "kind_acc": v["kind_accuracy"],
                "start_f1": v["starts"]["f1"], "drop_f1": v["drops"]["f1"]}  # fmt: skip
        print(json.dumps({k: round(x, 4) for k, x in line.items()}), flush=True)
        if best is None or score > best[0]:
            best = (score, epoch)
            torch.save(model.state_dict(), DATA / f"model_{args.tag}.pt")
    RES.mkdir(parents=True, exist_ok=True)
    r0 = [structure.baseline_r0(s["x"]) for s in va]
    meta = {"tag": args.tag, "config": vars(args), "best_val_score": best[0], "best_epoch": best[1],
            "val_r0": scores([k for k, _ in r0], [s for _, s in r0], va), "log": log}  # fmt: skip
    (RES / f"train_{args.tag}.json").write_text(json.dumps(meta, indent=1, default=float) + "\n")


def test_cmd(args: argparse.Namespace) -> None:
    import torch

    device = "cpu"
    cfg = json.loads((RES / f"train_{args.tag}.json").read_text())["config"]
    model = structure.structure_model(cfg["hidden"]).to(device)
    model.load_state_dict(torch.load(DATA / f"model_{args.tag}.pt", map_location=device))
    te = [s for s in load() if s["split"] == "test"]
    kinds, starts = run_model(model, te, device)
    r0 = [structure.baseline_r0(s["x"]) for s in te]
    result = {"tag": args.tag, "songs": len(te), "bars": int(sum(len(s["kind"]) for s in te)),
              "r0": scores([k for k, _ in r0], [s for _, s in r0], te),
              "r1": scores(kinds, starts, te)}  # fmt: skip
    (RES / f"test_{args.tag}.json").write_text(json.dumps(result, indent=1, default=float) + "\n")
    print(json.dumps(result, indent=1, default=float))


def main() -> None:
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    d = sub.add_parser("data")
    d.add_argument("--usb", type=Path, required=True)
    d.add_argument("--workers", type=int, default=8)
    t = sub.add_parser("train")
    t.add_argument("--tag", required=True)
    t.add_argument("--lr", type=float, default=1e-3)
    t.add_argument("--hidden", type=int, default=64)
    t.add_argument("--epochs", type=int, default=40)
    t.add_argument("--seed", type=int, default=0)
    s = sub.add_parser("test")
    s.add_argument("--tag", required=True)
    args = ap.parse_args()
    if args.cmd == "data":
        data_cmd(args.usb, args.workers)
    elif args.cmd == "train":
        train_cmd(args)
    else:
        test_cmd(args)


if __name__ == "__main__":
    main()
