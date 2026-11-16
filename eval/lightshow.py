"""Build the light show of a recorded set (prototype.lights) and a page to
watch it with the set's audio (docs/design/light-show.md).

  python -m eval.lightshow --verify data/mine/set.d57.json --set set.wav \\
      --index data/segments/mine_b2 --usb ~/Desktop --out data/mine/lights/set

Uses the entries that passed the audio check (D57: cmd/tracklist -json
-verify, or eval.verify_set's output), each song's Rekordbox
beat grid and phrases from the USB export, and the grid's offset against the
song's own audio (eval.beat_align). With --structure TAG, songs Rekordbox
gave no phrases get the structure model's (eval.structure_train,
docs/design/structure.md). Writes <out>.dmx (frames x channels,
uint8, prototype.lights.FPS a second), <out>.cues.json (each entry's phrases
and drops in set seconds, and which entries had no analysis), and
<out>.html, a page that plays the set and draws the rig in step with it.
Everything under data/ stays out of git.
"""

import argparse
import json
import shutil
from pathlib import Path

import numpy as np

from eval.beat_align import best_lag, checked_entries, onsets
from prototype import anlz, b2, lights, structure
from prototype.audio import decode

STRUCTURE = Path(__file__).resolve().parent.parent / "data" / "structure"


def predicted(path: Path, beats: list[anlz.Beat], offset_s: float, model) -> anlz.Structure:
    """The structure model's phrases for a song, on its corrected grid."""
    power = 10.0 ** (b2.spectrogram(decode(path)).astype(np.float64) / 10.0)
    kind, start = structure.predict(
        model, structure.bar_features(power, structure.bars(beats, offset_s))
    )
    return structure.to_structure(kind, start, beats)


def load_structure(tag: str):
    import torch

    cfg = json.loads((Path(__file__).resolve().parent / "results" / "structure"
                      / f"train_{tag}.json").read_text())["config"]  # fmt: skip
    model = structure.structure_model(cfg["hidden"])
    model.load_state_dict(torch.load(STRUCTURE / f"model_{tag}.pt", map_location="cpu"))
    return model


PAGE = Path(__file__).resolve().parent / "lightshow.html"


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--verify", type=Path, required=True)
    ap.add_argument("--set", type=Path, required=True)
    ap.add_argument("--index", type=Path, required=True)
    ap.add_argument("--usb", type=Path, required=True)
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--structure", help="structure model tag, for songs without phrases")
    args = ap.parse_args()
    model = load_structure(args.structure) if args.structure else None
    lib = anlz.library(args.usb)
    paths = {
        int(x["dense"]): Path(x["path"])
        for x in json.loads((args.index / "extras.json").read_text())
    }
    offsets_file = args.out.parent / "grid_offsets.json"
    offsets = json.loads(offsets_file.read_text()) if offsets_file.exists() else {}
    songs, cues, missing = [], [], []
    for e in checked_entries(args.verify):
        p = paths[e["track"]]
        try:
            key = "/" + str(p.relative_to(args.usb))
        except ValueError:
            key = ""
        if key not in lib:
            missing.append({"start_s": e["start_s"], "end_s": e["end_s"]})
            continue
        dat, ext = lib[key]
        beats = anlz.beats(dat.read_bytes())
        st = anlz.structure(ext.read_bytes()) if ext.exists() else None
        if key not in offsets:
            grid = np.array([b.time_s for b in beats])
            offsets[key] = best_lag(onsets(decode(p)), grid)
        source = "rekordbox" if st is not None else None
        if st is None and model is not None:
            st, source = predicted(p, beats, offsets[key] / 1000.0, model), "model"
        c = lights.song_cues(e["start_s"], e["end_s"], e["a"], e["rate"], beats, st,
                             offsets[key] / 1000.0)  # fmt: skip
        songs.append(c)
        cues.append({"start_s": c.start, "end_s": c.end, "phrases": c.phrases, "drops": c.drops,
                     "phrases_from": source})  # fmt: skip
    args.out.parent.mkdir(parents=True, exist_ok=True)
    offsets_file.write_text(json.dumps(offsets, indent=1) + "\n")
    duration = len(decode(args.set)) / 11025
    frames = lights.render(songs, duration)
    Path(f"{args.out}.dmx").write_bytes(frames.tobytes())
    meta = {"fps": lights.FPS, "channels": lights.CHANNELS, "washes": lights.WASHES,
            "heads": lights.HEADS, "frames": len(frames), "audio": args.set.name,
            "entries": cues, "no_analysis": missing}  # fmt: skip
    Path(f"{args.out}.cues.json").write_text(json.dumps(meta, indent=1) + "\n")
    page = Path(f"{args.out}.html")
    shutil.copy(PAGE, page)
    audio = args.out.parent / args.set.name
    if not audio.exists():
        audio.symlink_to(args.set)
    by = {k: sum(c["phrases_from"] == k for c in cues) for k in ("rekordbox", "model", None)}
    print(f"{len(songs)} songs with a grid ({by['rekordbox']} Rekordbox phrases, {by['model']} "
          f"predicted, {by[None]} none), {len(missing)} without; "
          f"{sum(len(c.drops) for c in songs)} drops; {len(frames)} frames -> {page}")  # fmt: skip


if __name__ == "__main__":
    main()
