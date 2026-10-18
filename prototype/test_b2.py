import numpy as np
import pytest

from prototype import b2, b2_match
from prototype.audio import SAMPLE_RATE


def test_filterbank_rows_sum_to_one_and_peak_at_their_centers() -> None:
    p = b2.DEFAULT
    fb = b2.filterbank(p.n_fft, p.bands_per_octave, p.fmin, p.n_bands)
    assert fb.shape == (p.n_bands, p.n_fft // 2 + 1)
    np.testing.assert_allclose(fb.sum(axis=1), 1.0)
    freqs = np.fft.rfftfreq(p.n_fft, 1 / SAMPLE_RATE)
    for band in (60, 120, 200):
        center = p.fmin * 2 ** (band / p.bands_per_octave)
        assert abs(freqs[np.argmax(fb[band])] - center) < freqs[1]


def test_tone_lands_in_its_band() -> None:
    p = b2.DEFAULT
    hz = p.fmin * 2 ** (150 / p.bands_per_octave)
    x = np.sin(2 * np.pi * hz * np.arange(SAMPLE_RATE * 2) / SAMPLE_RATE)
    spec = b2.spectrogram(x)
    assert abs(int(np.argmax(spec[len(spec) // 2])) - 150) <= 1


def peaks_to_landmarks(t: list[int], f: list[int]) -> b2.Landmarks:
    return b2.triplets(np.array(t, dtype=np.int64), np.array(f, dtype=np.int64))


def test_triplet_hash_survives_a_turntable_change() -> None:
    # Three peaks; a tempo change scales the time gaps and, on a log axis,
    # slides every band by the same amount. Gaps chosen so the ratio stays in
    # its quantization bin: 10/40 -> 16 of 64 either way.
    a = peaks_to_landmarks([100, 110, 140], [120, 130, 110])
    b = peaks_to_landmarks([200, 215, 260], [123, 133, 113])  # 1.5x slower, 3 bands up
    assert len(a.hash) == len(b.hash) == 1
    assert int(a.hash[0]) == int(b.hash[0])
    assert (int(a.span[0]), int(b.span[0])) == (40, 60)


def test_triplet_fields() -> None:
    lm = peaks_to_landmarks([0, 16, 32], [40, 50, 30])
    p = b2.DEFAULT
    h = int(lm.hash[0])
    assert h >> 20 == 40 // p.band_width
    assert (h >> 14) & 63 == 32  # 16 / 32 * 64
    assert (h >> 7) & 127 == 10 + p.zone and h & 127 == -10 + p.zone


@pytest.mark.parametrize("scale", [1.0, 1.05, 0.93])
def test_search_recovers_track_scale_and_offset(scale: float) -> None:
    rng = np.random.default_rng(1)
    tracks = []
    for _ in range(5):  # random constellations, 3 peaks per frame step of ~5 frames
        t = np.sort(rng.integers(0, 1000, 400))
        f = rng.integers(20, 200, 400)
        o = np.lexsort((f, t))
        tracks.append((t[o], f[o]))
    idx = b2_match.build([b2.triplets(t, f) for t, f in tracks])
    t, f = tracks[2]
    # The query plays track 2 from reference frame 300, `scale` times faster:
    # query frame q shows reference frame 300 + scale * q.
    sel = (t >= 300) & (t < 700)
    tq = np.round((t[sel] - 300) / scale).astype(np.int64)
    fq = f[sel] + int(round(36 * np.log2(scale)))  # turntable: bands slide
    o = np.lexsort((fq, tq))
    r = b2_match.search(idx, b2.triplets(tq[o], fq[o]))
    top = r.candidates[0]
    assert top.track == 2
    assert abs(top.scale - scale) < 0.01
    assert abs(top.offset - 300) < 3


def test_scale_table_is_powers_of_the_bin_ratio() -> None:
    table = b2_match.scale_table()
    mid = b2_match.TABLE_BINS
    assert table[mid] == 1.0 and table[mid + 1] == b2_match.SCALE_RATIO
    assert np.all(np.diff(table) > 0)
    np.testing.assert_allclose(table[mid + 8], 1.02**8, rtol=1e-14)
    assert b2_match.floor_bin(np.array([1.0, 1.0199, 1.02]))[[0, 1, 2]].tolist() == [0, 0, 1]
    assert b2_match.ceil_bin(np.array([1.0, 1.0001, 1.02]))[[0, 1, 2]].tolist() == [0, 1, 1]


@pytest.mark.parametrize(
    ("span_ref", "span_query", "bins"),
    [
        (63, 63, (-1, 1)),  # ratio known to +/-1.6%: the bins it reaches
        (10, 10, (-1, 2)),  # +/-10%: capped to 4 bins around the ratio
        (52, 50, (0, 3)),  # ratio 1.04, known to +/-2%: bins 0 .. 3
        (2, 3, None),  # ratio 0.67, far below the searched range: no votes
        (0, 5, None),
        (5, 0, None),
    ],
)
def test_vote_windows(span_ref: int, span_query: int, bins: tuple[int, int] | None) -> None:
    first, last = b2_match.windows()
    f, la = int(first[span_ref, span_query]), int(last[span_ref, span_query])
    if bins is None:
        assert f > la
    else:
        assert (f, la) == bins
    ok = first <= last
    assert first[ok].min() >= b2_match.BIN_MIN and last[ok].max() <= b2_match.BIN_MAX


def test_fit_line_is_exact_on_integer_lines() -> None:
    x = np.array([0, 5, 9, 20], dtype=np.int64)
    assert b2_match.fit_line(x, 300 + x) == (1.0, 300.0)
    assert b2_match.fit_line(x, 7 + 2 * x) == (2.0, 7.0)
    assert b2_match.fit_line(np.array([4, 4]), np.array([1, 9])) is None
    assert b2_match.fit_line(np.array([4]), np.array([1])) is None
