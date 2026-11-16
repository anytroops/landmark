"""The learned mixer-moves model (docs/design/mixer-moves.md): a temporal
convolutional network over a song's frames that corrects B1's per-band gains.

Input per frame: prototype.mixer.features (the set's, the song's, and the
other songs' sub-band log powers, and B1's gains in dB / 20). Output: the
song's gain in each mixer band, in dB / 20, as B1's plus a learned
correction, clamped to the label's range. Non-causal: it reads seconds of
context on both sides, as an offline analysis may.
"""

import torch
from torch import nn

from prototype.mixer import FLOOR_DB

IN_CHANNELS = 75
B1_SLICE = slice(72, 75)  # the features' B1 gains, dB / 20
LOW, HIGH = FLOOR_DB / 20.0, 12.0 / 20.0


class Block(nn.Module):
    def __init__(self, ch: int, kernel: int, dilation: int) -> None:
        super().__init__()
        self.norm = nn.GroupNorm(8, ch)
        self.conv = nn.Conv1d(
            ch, ch, kernel, dilation=dilation, padding=dilation * (kernel - 1) // 2
        )
        self.mix = nn.Conv1d(ch, ch, 1)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return x + self.mix(nn.functional.gelu(self.conv(self.norm(x))))


class MixerTCN(nn.Module):
    """(batch, IN_CHANNELS, frames) to (batch, 3, frames): gains in dB / 20."""

    def __init__(self, ch: int = 64, layers: int = 6, kernel: int = 5) -> None:
        super().__init__()
        self.inp = nn.Conv1d(IN_CHANNELS, ch, 1)
        self.blocks = nn.ModuleList(Block(ch, kernel, 2**i) for i in range(layers))
        self.out = nn.Conv1d(ch, 3, 1)
        nn.init.zeros_(self.out.weight)  # starts as B1
        nn.init.zeros_(self.out.bias)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        h = self.inp(x)
        for b in self.blocks:
            h = b(h)
        return torch.clamp(x[:, B1_SLICE] + self.out(h), LOW, HIGH)


def receptive_field(layers: int = 6, kernel: int = 5) -> int:
    """Frames of context the network sees around each frame."""
    return 1 + sum((kernel - 1) * 2**i for i in range(layers))


def song_gains(model: MixerTCN, mix, sources: list, b1, device: str = "cpu"):
    """Every song's gains (songs x frames x 3, linear amplitude) by the
    model, over the frames where its warped power is nonzero (zero
    elsewhere), from the set's power, the warped songs, and B1's gains."""
    import numpy as np

    from prototype.mixer import features

    out = np.zeros_like(b1)
    model.eval()
    with torch.no_grad():
        for k, s in enumerate(sources):
            on = np.nonzero(s.sum(axis=1) > 0)[0]
            if len(on) == 0:
                continue
            sl = slice(on[0], on[-1] + 1)
            x = torch.from_numpy(features(mix, sources, b1, k)[sl].T[None].copy()).to(device)
            db = model(x)[0].T.cpu().numpy() * 20.0
            out[k, sl] = 10.0 ** (db / 20.0)
            out[k][s.sum(axis=1) <= 0] = 0.0
    return out
