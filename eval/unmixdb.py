"""Prepare UnmixDB (Schwarz and Fourer, ISMIR 2018 LBD) for the tracklist eval.

  python -m eval.unmixdb prepare --root data/unmixdb/extracted

Writes:
- data/unmixdb/refs.tsv: every reference excerpt as name<TAB>path, the
  input to `cmd/ingest -extra` (names are set/file, deduplicated by file name
  across sets, since a track can appear in more than one source mix);
- eval/mixes/unmixdb.json: ground truth in eval.mixgen's format, one entry
  per mix with audio, converted to 11,025 Hz mono WAV under data/unmixdb/wav/.

UnmixDB labels give, per track of a 3-track mix: start (mix time where
excerpt time 0 would be), speed (playback rate, < 1 is slower), fadein,
fadeout, stop. A track is audible from its fade-in start to its fade-out end;
excerpt time at mix time t is (t - start) * speed. Tempo variants map to ours:
"resample" is turntable ("speed"), "stretch" (sox's tempo effect, WSOLA) is
keylock, "none" leaves tempo alone. Some of the published mix files are
truncated (e.g. set222mix3-resample-compressor-07 holds 67 s of audio while
its labels run to 112 s), so each segment is clipped to the audio actually
present, and one left with under --min-audible seconds is marked
"ignore": it neither counts toward recall nor makes a matching entry a
false positive. `dense` is the track's position in
refs.tsv; add the number of tracks indexed before the extras (e.g. 23,985
for fma_medium) when the index has them first (--dense-offset).
"""

import argparse
import json
import re
import subprocess
from pathlib import Path

import numpy as np
import soundfile as sf

ROOT = Path(__file__).resolve().parent.parent
MIX = re.compile(
    r"(set\d+)mix(\d+)-(none|resample|stretch)-(none|bass|compressor|distortion)-(\d+)"
)
MODES = {"none": "none", "resample": "speed", "stretch": "keylock"}


def parse_labels(text: str) -> dict[int, dict]:
    """Track number -> {start, speed, fadein, fadeout, stop, file}."""
    tracks: dict[int, dict] = {}
    for line in text.splitlines():
        cols = line.split("\t")
        if len(cols) < 4:
            continue
        a, b, n, key = float(cols[0]), float(cols[1]), int(cols[2]), cols[3]
        value = cols[4].strip().strip('"') if len(cols) > 4 else ""
        t = tracks.setdefault(n, {})
        if key in ("start", "stop"):
            t[key] = a
            if value:
                t["file"] = value
        elif key == "speed":
            t["speed"] = float(value)
        elif key in ("fadein", "fadeout"):
            t[key] = (a, b)
    return tracks


def to_wav(src: Path, dst: Path) -> None:
    dst.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(["ffmpeg", "-nostdin", "-v", "error", "-y", "-i", str(src), "-ac", "1",
                    "-ar", "11025", "-c:a", "pcm_s16le", str(dst)], check=True)  # fmt: skip


def prepare(root: Path, dense_offset: int, min_audible: float, name: str,
            sample: int, seed: int) -> None:  # fmt: skip
    refs: dict[str, tuple[int, Path]] = {}  # excerpt file name -> (index, path)
    for ref in sorted(root.glob("mixotic-set*-excerpts/refsongs/*.excerpt40.mp3")):
        if ref.name not in refs:
            refs[ref.name] = (len(refs), ref)
    lines = [
        f"{p.parent.parent.name.split('-')[1]}/{name}\t{p.resolve()}"
        for name, (_, p) in refs.items()
    ]
    (ROOT / "data" / "unmixdb" / f"{name}_refs.tsv").write_text("\n".join(lines) + "\n")

    all_mixes = sorted(root.glob("mixotic-set*-excerpts/mixes/*.mp3"))
    names = [MIX.fullmatch(p.name.removesuffix(".mp3")) for p in all_mixes]
    if any(m is None for m in names):
        bad = next(p for p, m in zip(all_mixes, names, strict=True) if m is None)
        raise SystemExit(f"unexpected mix name {bad.name}")
    if sample:  # a seeded sample of playlists per set, each with all 12 versions
        rng = np.random.default_rng(seed)
        chosen = set()
        for st in sorted({m.group(1) for m in names}):
            pls = sorted({int(m.group(5)) for m in names if m.group(1) == st})
            chosen |= {(st, int(x)) for x in rng.permutation(pls)[:sample]}
        keep = [(p, m) for p, m in zip(all_mixes, names, strict=True)
                if (m.group(1), int(m.group(5))) in chosen]  # fmt: skip
    else:
        keep = list(zip(all_mixes, names, strict=True))
    mixes = []
    for mp3, m in keep:
        labels = parse_labels(
            mp3.with_name(mp3.name.removesuffix(".mp3") + ".labels.txt").read_text()
        )
        wav = ROOT / "data" / "unmixdb" / "wav" / (mp3.stem + ".wav")
        if not wav.exists():
            to_wav(mp3, wav)
        audio_s = sf.info(wav).duration
        segments = []
        for n in sorted(labels):
            t = labels[n]
            if t["file"] not in refs:
                raise SystemExit(f"{mp3.name}: track {t['file']} is not among the reference songs")
            first, last = n == min(labels), n == max(labels)
            fin = t.get("fadein", (t["start"], t["start"]))
            fout = t.get("fadeout", (t["stop"], t["stop"]))
            mix_start, mix_end = max(fin[0], 0.0), fout[1] if not last else t["stop"]
            labeled_end = mix_end
            mix_end = min(mix_end, audio_s)
            speed = t.get("speed", 1.0)
            segments.append({
                "fma_id": t["file"], "dense": dense_offset + refs[t["file"]][0], "known": True,
                "mode": MODES[m.group(3)], "pct": round((speed - 1) * 100, 4), "factor": speed,
                "src_start": round((mix_start - t["start"]) * speed, 3),
                "mix_start": round(mix_start, 3), "mix_end": round(mix_end, 3),
                "fade_in": 0.0 if first else round(fin[1] - fin[0], 3),
                "fade_out": 0.0 if last or mix_end < labeled_end else round(fout[1] - fout[0], 3),
                "ignore": mix_end - mix_start < min_audible,
            })  # fmt: skip
        mixes.append({"file": str(wav.relative_to(ROOT)), "name": mp3.stem, "set": m.group(1),
                      "tracks_per_mix": int(m.group(2)),
                      "audio_s": round(audio_s, 3),
                      "labeled_s": round(max(labels[n]["stop"] for n in labels), 3),
                      "playlist": int(m.group(5)), "timescale": m.group(3), "effect": m.group(4),
                      "segments": segments})  # fmt: skip
    gt = {"name": name, "source": "UnmixDB v1, zenodo.org/records/1422385",
          "dense_offset": dense_offset, "references": len(refs), "sample_rate": 11025,
          "min_audible_s": min_audible, "sample_playlists_per_set": sample, "sample_seed": seed,
          "mixes": mixes}  # fmt: skip
    out = ROOT / "eval" / "mixes" / f"{name}.json"
    out.write_text(json.dumps(gt, indent=1) + "\n")
    short = sum(1 for m in mixes if m["labeled_s"] - m["audio_s"] > 5)
    ignored = sum(s["ignore"] for m in mixes for s in m["segments"])
    print(
        f"{len(refs)} reference songs, {len(mixes)} mixes ({short} truncated by over 5 s, "
        f"{ignored} segments ignored) -> {out}"
    )


def main() -> None:
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("prepare")
    p.add_argument("--root", type=Path, default=ROOT / "data/unmixdb/extracted")
    p.add_argument("--dense-offset", type=int, default=0)
    p.add_argument("--min-audible", type=float, default=10.0)
    p.add_argument("--name", default="unmixdb", help="output set name (eval/mixes/<name>.json)")
    p.add_argument("--sample", type=int, default=0, help="playlists per set to keep (0 = all)")
    p.add_argument("--seed", type=int, default=0)
    args = ap.parse_args()
    prepare(args.root, args.dense_offset, args.min_audible, args.name, args.sample, args.seed)


if __name__ == "__main__":
    main()
