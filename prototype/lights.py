"""A light show for a set, from what Landmark knows about it
(docs/design/light-show.md): which song plays where (the verified
tracklist), at what tempo and position (its alignment), and each song's
beats and phrases (Rekordbox's analysis of the DJ's library, prototype.anlz,
its grid corrected against the song's own audio, eval.beat_align).

cue_track: the set's cue events in set time: every beat and downbeat, every
phrase change with its kind, the drop (the first chorus after a build), and
each song's share of the mix. render: those cues as DMX channel values for a
virtual rig, FPS frames a second: 8 RGB washes, 4 moving heads, a strobe.
Anticipation is built in: every cue is known before its moment, so a live
player can send it early by the output's latency.
"""

import colorsys
from dataclasses import dataclass, field

import numpy as np

from prototype import anlz

FPS = 40  # DMX frames a second (DMX512 tops out near 44)
WASHES, HEADS = 8, 4
# DMX layout of the virtual rig, one universe: each wash R G B dimmer; each
# head pan tilt dimmer R G B; the strobe dimmer and rate.
CHANNELS = WASHES * 4 + HEADS * 6 + 2

# Each phrase kind's look: hue (0 to 1), saturation, base intensity, how much
# beats pulse, and head motion speed (cycles a bar).
LOOKS = {
    "intro": (0.70, 0.9, 0.25, 0.25, 0.10),
    "verse": (0.52, 0.8, 0.45, 0.40, 0.20),
    "up": (0.08, 0.7, 0.45, 0.60, 0.35),
    "chorus": (0.90, 1.0, 0.85, 0.75, 0.50),
    "down": (0.62, 0.9, 0.20, 0.20, 0.08),
    "bridge": (0.35, 0.8, 0.40, 0.35, 0.15),
    "outro": (0.75, 0.8, 0.25, 0.25, 0.10),
    "none": (0.58, 0.6, 0.40, 0.50, 0.20),  # a song without phrase analysis
}


@dataclass
class SongCues:
    """One tracklist entry's cues in set seconds."""

    start: float
    end: float
    beats: np.ndarray  # set seconds
    bar: np.ndarray  # 1 to 4
    phrases: list[tuple[float, str]] = field(default_factory=list)  # (set seconds, kind)
    drops: list[float] = field(default_factory=list)


def song_cues(start: float, end: float, a: float, rate: float, beats: list[anlz.Beat],
              structure: anlz.Structure | None, grid_offset_s: float) -> SongCues:  # fmt: skip
    """The entry's beats, phrases, and drops in set seconds: song seconds
    s map to set seconds (s - a) / rate; the grid shifted by its offset
    against the song's own audio first."""
    song_t = np.array([b.time_s for b in beats]) + grid_offset_s
    set_t = (song_t - a) / rate
    bar = np.array([b.number for b in beats])
    c = SongCues(start, end, set_t, bar)
    if structure is not None:
        prev = ""
        for p in structure.phrases:
            if 1 <= p.beat <= len(set_t):
                t = float(set_t[p.beat - 1])
                c.phrases.append((t, p.kind))
                if p.kind == "chorus" and prev in ("up", "verse", "bridge") and start <= t <= end:
                    c.drops.append(t)
                prev = p.kind
    return c


def kind_at(c: SongCues, t: float) -> str:
    kind = "none" if not c.phrases else c.phrases[0][1]
    for when, k in c.phrases:
        if when <= t:
            kind = k
    return kind


def shares(songs: list[SongCues], t: np.ndarray) -> np.ndarray:
    """Each song's share of the mix at times t (songs x frames): 1 inside
    its entry, crossfading linearly where two entries overlap."""
    w = np.zeros((len(songs), len(t)))
    for i, c in enumerate(songs):
        inside = (t >= c.start) & (t <= c.end)
        w[i, inside] = 1.0
    for i in range(len(songs)):
        for j in range(len(songs)):
            if i == j:
                continue
            a, b = songs[i], songs[j]
            lo, hi = max(a.start, b.start), min(a.end, b.end)
            if hi <= lo or not (a.start < b.start):
                continue
            o = (t >= lo) & (t <= hi)
            u = (t[o] - lo) / (hi - lo)
            w[i, o] = np.minimum(w[i, o], 1.0 - u)
            w[j, o] = np.minimum(w[j, o], u)
    total = w.sum(axis=0)
    return np.where(total > 0, w / np.maximum(total, 1e-9), 0.0)


def pulse(t: np.ndarray, beats: np.ndarray, decay_s: float) -> np.ndarray:
    """1 at each beat, decaying exponentially until the next."""
    idx = np.searchsorted(beats, t, side="right") - 1
    since = np.where(idx >= 0, t - beats[np.maximum(idx, 0)], np.inf)
    return np.exp(-since / decay_s)


def render(songs: list[SongCues], duration: float) -> np.ndarray:
    """DMX frames (frames x CHANNELS, uint8) of the show."""
    t = np.arange(int(duration * FPS)) / FPS
    w = shares(songs, t)
    out = np.zeros((len(t), CHANNELS))
    for i, c in enumerate(songs):
        on = w[i] > 0
        if not on.any() or len(c.beats) < 2:
            continue
        tt = t[on]
        kinds = np.array([kind_at(c, x) for x in tt])
        look = np.array([LOOKS.get(k, LOOKS["none"]) for k in kinds])
        hue, sat, base, beat_amt, motion = look.T
        beat = pulse(tt, c.beats, 0.12)
        down = pulse(tt, c.beats[c.bar == 1], 0.25)
        level = np.clip(base + beat_amt * (0.6 * beat + 0.4 * down), 0, 1)
        # A build brightens toward its end; the drop flashes the rig white.
        drop = np.zeros_like(tt)
        for d in c.drops:
            drop = np.maximum(drop, np.where(tt >= d, np.exp(-(tt - d) / 0.8), 0.0))
        bar_n = np.searchsorted(c.beats[c.bar == 1], tt, side="right")
        for k in range(WASHES):
            h = (hue + 0.08 * ((k + bar_n) % 3)) % 1.0
            rgb = np.array([colorsys.hsv_to_rgb(a, b, 1.0) for a, b in zip(h, sat, strict=True)])
            rgb = rgb * (1 - drop[:, None]) + drop[:, None]
            out[on, 4 * k : 4 * k + 3] += w[i, on][:, None] * rgb * 255
            out[on, 4 * k + 3] += w[i, on] * np.clip(level + drop, 0, 1) * 255
        beats_in = np.interp(tt, c.beats, np.arange(len(c.beats)))
        for k in range(HEADS):
            base_ch = WASHES * 4 + 6 * k
            phase = beats_in / 4 * motion * 2 * np.pi + k * np.pi / 2
            out[on, base_ch] += w[i, on] * (127 + 100 * np.sin(phase))
            out[on, base_ch + 1] += w[i, on] * (127 + 60 * np.cos(phase))
            out[on, base_ch + 2] += w[i, on] * np.clip(level * (kinds != "down") + drop, 0, 1) * 255
            rgb = np.array(
                [colorsys.hsv_to_rgb((a + 0.5) % 1, b, 1.0) for a, b in zip(hue, sat, strict=True)]
            )
            out[on, base_ch + 3 : base_ch + 6] += w[i, on][:, None] * rgb * 255
        strobe = WASHES * 4 + HEADS * 6
        build_end = np.zeros_like(tt)
        for when, k in c.phrases:
            if k == "up":
                nxt = [x for x, _ in c.phrases if x > when]
                end = nxt[0] if nxt else c.end
                ramp = np.clip((tt - (end - 8)) / 8, 0, 1) * (tt < end)
                build_end = np.maximum(build_end, ramp)
        out[on, strobe] += w[i, on] * np.clip(build_end + drop, 0, 1) * 255
        out[on, strobe + 1] += (
            w[i, on] * np.clip(0.3 + 0.7 * build_end, 0, 1) * 255 * (build_end + drop > 0)
        )
    return np.clip(out, 0, 255).astype(np.uint8)
