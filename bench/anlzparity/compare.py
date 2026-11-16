"""Compare internal/rekordbox (bench/anlzparity's JSON) with prototype/anlz.py
on every analysed song of a USB export; prints counts only.

  go run ./bench/anlzparity -usb ~/Desktop > go.json
  python bench/anlzparity/compare.py --usb ~/Desktop --go go.json
"""

import argparse
import json
from pathlib import Path

from prototype import anlz


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--usb", type=Path, required=True)
    ap.add_argument("--go", type=Path, required=True)
    ap.add_argument("--out", type=Path)
    args = ap.parse_args()
    go = json.loads(args.go.read_text())
    lib = anlz.library(args.usb)
    same = beats = phrased = 0
    differ = []
    for key, (dat, ext) in sorted(lib.items()):
        b = [[x.number, x.bpm, x.time_s] for x in anlz.beats(dat.read_bytes())]
        st = anlz.structure(ext.read_bytes()) if ext.exists() else None
        g = go.get(key)
        ok = (
            g is not None
            and len(g["beats"]) == len(b)
            and all(
                gb[0] == pb[0] and abs(gb[1] - pb[1]) < 1e-9 and abs(gb[2] - pb[2]) < 1e-9
                for gb, pb in zip(g["beats"], b, strict=True)
            )
        )
        if st is None:
            ok = ok and not g.get("phrases")
        else:
            phrased += 1
            ok = (
                ok
                and g.get("mood") == st.mood
                and g.get("end_beat") == st.end_beat
                and (
                    [tuple(p) for p in g.get("phrases", [])]
                    == [(p.beat, p.kind) for p in st.phrases]
                )
            )
        same += ok
        beats += len(b)
        if not ok:
            differ.append(key)
    result = {"songs": len(lib), "go_songs": len(go), "same": same, "beats": beats,
              "with_phrases": phrased, "differ": len(differ)}  # fmt: skip
    print(json.dumps(result))
    if args.out:
        args.out.write_text(json.dumps(result, indent=1) + "\n")


if __name__ == "__main__":
    main()
