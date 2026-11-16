"""Read Rekordbox's per-track analysis files (USB export, PIONEER/USBANLZ):
the beat grid and the phrase structure, for the lighting work
(docs/design/light-show.md).

An ANLZ file is a "PMAI" header and tagged sections, big-endian. ANLZ0000.DAT
holds PPTH (the audio file's path on the USB) and PQTZ (the beat grid: for
each beat, its number in the bar, the tempo, and its time); ANLZ0000.EXT holds
PSSI (the phrases), which Rekordbox 6 and later store XOR-masked. The layout
follows Deep Symmetry's public description of the format (dysentery and
crate-digger), checked against Sean's export.
"""

import struct
from dataclasses import dataclass
from pathlib import Path

MASK = bytes.fromhex("CBE1EEFAE5EEADEEE9D2E9EBE1E9F3E8E9F4E1")
MASK_FROM = 18  # PSSI bytes from the mood field on are masked

# Phrase kinds by mood (PSSI); Rekordbox's names.
MOODS = {1: "high", 2: "mid", 3: "low"}
KINDS = {
    1: {1: "intro", 2: "up", 3: "down", 5: "chorus", 6: "outro"},
    2: {1: "intro", 2: "verse", 3: "verse", 4: "verse", 5: "verse", 6: "verse", 7: "verse",
        8: "bridge", 9: "chorus", 10: "outro"},
    3: {1: "intro", 2: "verse", 3: "verse", 4: "verse", 5: "verse", 6: "verse", 7: "verse",
        8: "bridge", 9: "chorus", 10: "outro"},
}  # fmt: skip


@dataclass(frozen=True)
class Beat:
    number: int  # 1 to 4: its place in the bar (1 is the downbeat)
    bpm: float
    time_s: float


@dataclass(frozen=True)
class Phrase:
    beat: int  # the beat (1-based, into the grid) it starts on
    kind: str


@dataclass(frozen=True)
class Structure:
    mood: str
    end_beat: int
    phrases: list[Phrase]


def tags(data: bytes) -> dict[str, tuple[int, int, int]]:
    """fourcc to (offset, header length, tag length) of each section (the
    first of a kind)."""
    if data[:4] != b"PMAI":
        raise ValueError("not an ANLZ file")
    header, _ = struct.unpack(">II", data[4:12])
    out: dict[str, tuple[int, int, int]] = {}
    off = header
    while off + 12 <= len(data):
        fourcc = data[off : off + 4].decode("latin-1")
        lh, lt = struct.unpack(">II", data[off + 4 : off + 12])
        if lt < 12:
            break
        out.setdefault(fourcc, (off, lh, lt))
        off += lt
    return out


def audio_path(dat: bytes) -> str:
    off, _, _ = tags(dat)["PPTH"]
    n = struct.unpack(">I", dat[off + 12 : off + 16])[0]
    return dat[off + 16 : off + 16 + n].decode("utf-16-be").rstrip("\x00")


def beats(dat: bytes) -> list[Beat]:
    off, lh, _ = tags(dat)["PQTZ"]
    n = struct.unpack(">I", dat[off + 20 : off + 24])[0]
    body = dat[off + lh : off + lh + 8 * n]
    return [Beat(num, tempo / 100.0, ms / 1000.0)
            for num, tempo, ms in struct.iter_unpack(">HHI", body)]  # fmt: skip


def unmask(tag: bytes) -> bytes:
    """A PSSI tag with its masked bytes restored (masked tags only)."""
    n = struct.unpack(">H", tag[16:18])[0]
    out = bytearray(tag)
    for i in range(MASK_FROM, len(out)):
        out[i] ^= (MASK[(i - MASK_FROM) % len(MASK)] + n) & 0xFF
    return bytes(out)


def structure(ext: bytes) -> Structure | None:
    """The phrases, or None if the file has no PSSI tag. Masked or not is
    decided by which reading gives a valid mood."""
    t = tags(ext)
    if "PSSI" not in t:
        return None
    off, lh, lt = t["PSSI"]
    raw = ext[off : off + lt]
    for tag in (unmask(raw), raw):
        entry_len, n = struct.unpack(">IH", tag[12:18])
        mood = struct.unpack(">H", tag[18:20])[0]
        if mood not in MOODS or entry_len != 24:
            continue
        end_beat = struct.unpack(">H", tag[26:28])[0]
        phrases = []
        for i in range(n):
            e = tag[lh + 24 * i : lh + 24 * (i + 1)]
            _, beat, kind = struct.unpack(">HHH", e[:6])
            phrases.append(Phrase(beat, KINDS[mood].get(kind, f"kind{kind}")))
        return Structure(MOODS[mood], end_beat, phrases)
    raise ValueError("PSSI tag with no valid mood")


def library(usb: Path) -> dict[str, tuple[Path, Path]]:
    """Audio path on the USB (as PPTH gives it) to its (DAT, EXT) files."""
    out = {}
    for dat in sorted((usb / "PIONEER" / "USBANLZ").glob("*/*/ANLZ0000.DAT")):
        ext = dat.with_suffix(".EXT")
        out[audio_path(dat.read_bytes())] = (dat, ext)
    return out
