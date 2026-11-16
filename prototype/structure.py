"""Song structure on the beat grid (docs/design/structure.md): per bar of a
song, the phrase kind and whether a phrase starts there.

bar_features: from the triplet front end's spectrogram, pooled over each
bar's four beats. labels: Rekordbox's phrases (prototype.anlz) as per-bar
kinds and starts. baseline_r0: novelty peaks on 8-bar multiples, labelled by
energy. The learned model, R1, is StructureGRU.
"""

import numpy as np

from prototype import anlz, b2
from prototype.mixer import band_slices

KINDS = ("intro", "verse", "build", "drop", "breakdown", "bridge", "outro")
FPS = 11025 / b2.DEFAULT.hop
N_FEATURES = 24 * 2 + 2


def unify(kind: str, mood: str) -> str:
    """Rekordbox's kind, in KINDS: its high mood's chorus is the drop."""
    return {"up": "build", "down": "breakdown", "chorus": "drop"}.get(kind, kind)


def bars(beats: list[anlz.Beat], offset_s: float) -> np.ndarray:
    """Start times (s, in our decode's time) of the grid's bars: its
    downbeats, the grid shifted by its offset against the audio."""
    return np.array([b.time_s + offset_s for b in beats if b.number == 1])


def bar_features(power: np.ndarray, bar_starts: np.ndarray) -> np.ndarray:
    """bars x N_FEATURES: each bar's 24 sub-band log powers (mean over the
    bar), their change from the previous bar, the onset strength, and the
    low band's share of the power."""
    edges = np.round(np.append(bar_starts, bar_starts[-1] + np.median(np.diff(bar_starts)))
                     * FPS).astype(int)  # fmt: skip
    edges = np.clip(edges, 0, len(power))
    cols = []
    for sl in band_slices():
        for chunk in np.array_split(np.arange(sl.start, sl.stop), 8):
            cols.append(power[:, chunk].sum(axis=1))
    sub = np.stack(cols, axis=1)
    rise = np.maximum(np.diff(np.log10(sub + 1e-10), axis=0), 0).sum(axis=1)
    flux = np.concatenate([[0.0], rise])
    out = np.zeros((len(bar_starts), N_FEATURES))
    for i in range(len(bar_starts)):
        a, b = edges[i], max(edges[i + 1], edges[i] + 1)
        seg = sub[a:b] if a < len(sub) else np.full((1, sub.shape[1]), 1e-10)
        out[i, :24] = np.log10(seg.mean(axis=0) + 1e-10)
        out[i, 48] = flux[a:b].mean() if a < len(flux) else 0.0
        out[i, 49] = seg[:, :8].sum() / max(seg.sum(), 1e-10)
    out[1:, 24:48] = out[1:, :24] - out[:-1, :24]
    out[:, :24] -= np.median(out[:, :24], axis=0)  # the song's own level
    return out


def labels(structure: anlz.Structure, beats: list[anlz.Beat]) -> tuple[np.ndarray, np.ndarray]:
    """Per bar: the phrase kind (index into KINDS) and whether one starts."""
    downbeat_idx = [i + 1 for i, b in enumerate(beats) if b.number == 1]  # 1-based beat numbers
    kind = np.zeros(len(downbeat_idx), int)
    start = np.zeros(len(downbeat_idx), int)
    phrases = [(p.beat, KINDS.index(unify(p.kind, structure.mood))) for p in structure.phrases
               if unify(p.kind, structure.mood) in KINDS]  # fmt: skip
    for j, beat in enumerate(downbeat_idx):
        current = [k for b, k in phrases if b <= beat + 3]
        kind[j] = current[-1] if current else 0
        start[j] = any(abs(b - beat) <= 1 for b, _ in phrases)
    return kind, start


def novelty(feats: np.ndarray, width: int = 8) -> np.ndarray:
    """Foote's checkerboard novelty over bars, on the features' cosine
    self-similarity."""
    x = feats - feats.mean(axis=0)
    x /= np.linalg.norm(x, axis=1, keepdims=True) + 1e-9
    s = x @ x.T
    k = np.ones((2 * width, 2 * width))
    k[:width, width:] = k[width:, :width] = -1
    n = len(feats)
    out = np.zeros(n)
    for i in range(width, n - width):
        out[i] = (s[i - width : i + width, i - width : i + width] * k).sum()
    return out


def baseline_r0(feats: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
    """R0: phrase starts at novelty peaks on 8-bar multiples, each phrase
    labelled by its energy against the song's median."""
    n = len(feats)
    nov = novelty(feats)
    cand = [i for i in range(8, n - 4, 8) if nov[i] > 0 and nov[i] >= np.median(nov[nov > 0])]
    starts = [0] + cand
    energy = feats[:, :24].mean(axis=1)
    med = np.median(energy)
    kind = np.zeros(n, int)
    start = np.zeros(n, int)
    for j, a in enumerate(starts):
        b = starts[j + 1] if j + 1 < len(starts) else n
        start[a] = 1
        e = energy[a:b].mean() - med
        nxt = energy[b : b + 8].mean() - med if b < n else -1
        if j == 0 and e < 0:
            k = "intro"
        elif b >= n and e < 0:
            k = "outro"
        elif e < 0 and nxt > 0:
            k = "build"
        elif e >= 0:
            k = "drop"
        else:
            k = "breakdown"
        kind[a:b] = KINDS.index(k)
    return kind, start


def drops(kind: np.ndarray) -> list[int]:
    """Bars where a drop starts after a build, verse, or bridge."""
    d, before = KINDS.index("drop"), {KINDS.index(k) for k in ("build", "verse", "bridge")}
    return [i for i in range(1, len(kind)) if kind[i] == d and kind[i - 1] in before]


def structure_model(hidden: int = 64):
    """R1: a bidirectional GRU over a song's bars with a kind head and a
    phrase-start head."""
    import torch
    from torch import nn

    class StructureGRU(nn.Module):
        def __init__(self) -> None:
            super().__init__()
            self.inp = nn.Linear(N_FEATURES, hidden)
            self.gru = nn.GRU(hidden, hidden, num_layers=2, batch_first=True, bidirectional=True)
            self.kind = nn.Linear(2 * hidden, len(KINDS))
            self.start = nn.Linear(2 * hidden, 1)

        def forward(self, x: torch.Tensor) -> tuple[torch.Tensor, torch.Tensor]:
            h, _ = self.gru(torch.relu(self.inp(x)))
            return self.kind(h), self.start(h)[..., 0]

    return StructureGRU()


def predict(model, feats: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
    """R1's per-bar kinds and phrase starts for one song's bar features:
    starts where the start head's probability passes 0.5 at a local peak."""
    import torch

    model.eval()
    with torch.no_grad():
        k, st = model(torch.from_numpy(feats.astype(np.float32)[None]))
    p = torch.sigmoid(st[0]).numpy()
    peaks = (p > 0.5) & (p >= np.maximum(np.roll(p, 1), np.roll(p, -1)))
    return k[0].argmax(-1).numpy(), peaks.astype(int)


REKORDBOX = {"build": "up", "drop": "chorus", "breakdown": "down"}


def to_structure(kind: np.ndarray, start: np.ndarray, beats: list[anlz.Beat]) -> anlz.Structure:
    """Per-bar predictions as a Rekordbox-style phrase list (high mood's
    names): a phrase begins at the first bar, at each predicted start, and
    wherever the kind changes."""
    downbeats = [i + 1 for i, b in enumerate(beats) if b.number == 1]
    phrases = []
    for j in range(min(len(kind), len(downbeats))):
        if j == 0 or start[j] or kind[j] != kind[j - 1]:
            name = KINDS[kind[j]]
            phrases.append(anlz.Phrase(downbeats[j], REKORDBOX.get(name, name)))
    return anlz.Structure("high", len(beats), phrases)
