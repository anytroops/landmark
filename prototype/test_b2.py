from dataclasses import replace

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


def test_dual_query_adds_the_half_band_landmarks() -> None:
    x = np.random.default_rng(3).standard_normal(4 * SAMPLE_RATE)
    single = b2.fingerprint_query(x, dual=False)
    half = b2.fingerprint_query(x, p=b2.half_band(), dual=False)
    dual = b2.fingerprint_query(x)

    def keys(lm: b2.Landmarks) -> set[tuple[int, int, int, int]]:
        cols = (lm.hash.tolist(), lm.t.tolist(), lm.span.tolist(), lm.band.tolist())
        return set(zip(*cols, strict=True))

    assert keys(dual) == keys(single) | keys(half)
    assert len(dual.hash) == len(keys(dual))  # no duplicates
    order = np.lexsort((dual.band, dual.span, dual.t, dual.hash))
    assert np.array_equal(order, np.arange(len(order)))  # sorted by (hash, t, span, band)
    assert b2.half_band().fmin == pytest.approx(55 * 2 ** (1 / 72))


def test_flat_fit_is_not_a_match() -> None:
    # One reference posting per hash; the query repeats each hash at many
    # times, so every match has the same reference time: a flat line.
    hashes = np.arange(1, 21, dtype=np.uint32)
    ref = b2.Landmarks(hashes, np.full(20, 500, np.uint32), np.full(20, 30, np.uint8),
                       np.full(20, 50, np.uint8))  # fmt: skip
    idx = b2_match.build([ref])
    q = b2.Landmarks(np.repeat(hashes, 5), np.tile(np.arange(0, 25, 5, dtype=np.uint32), 20),
                     np.full(100, 30, np.uint8), np.full(100, 50, np.uint8))  # fmt: skip
    assert b2_match.search(idx, q).candidates == []


def test_prune_keeps_the_tracks_with_most_matches() -> None:
    track = np.array([5, 5, 5, 2, 2, 9, 9, 7, 2, 9], dtype=np.int64)  # 5: 3, 2: 3, 9: 3, 7: 1
    kept = b2_match.prune(track, 2)
    assert kept.tolist() == [True] * 5 + [False] * 3 + [True, False]
    assert sorted(set(track[kept].tolist())) == [2, 5]  # three-way tie at 3: smallest ids win
    assert b2_match.prune(track, 10).all()


def music_like_spec(seed: int = 0, frames: int = 430) -> np.ndarray:
    """A dB spectrogram with a quiet bed and scattered strong events."""
    rng = np.random.default_rng(seed)
    p = b2.DEFAULT
    spec = rng.normal(0, 3, (frames, p.n_bands))
    for _ in range(600):
        spec[rng.integers(frames), rng.integers(p.n_bands)] += rng.uniform(15, 40)
    return spec


NO_CAP = replace(b2.DEFAULT, peak_regions=1, region_cap=0)  # the fingerprint before D53


def test_robust_peaks_without_options_equal_find_peaks() -> None:
    from prototype.fingerprint import find_peaks

    spec = music_like_spec()
    a, b = find_peaks(spec, NO_CAP), b2.robust_peaks(spec, NO_CAP)
    np.testing.assert_array_equal(a.t, b.t)
    np.testing.assert_array_equal(a.f, b.f)


def test_normalizing_stops_a_steady_hum_taking_the_peaks() -> None:
    spec = music_like_spec()
    spec[:, 40] += 60  # a hum: one band, loud and steady
    spec[::9, 40] += 1  # rippling, so it has local maxima along time
    plain = b2.robust_peaks(spec, NO_CAP)
    normed = b2.robust_peaks(spec, replace(NO_CAP, norm_frames=129))
    assert (plain.f == 40).sum() > 20
    assert (normed.f == 40).sum() <= 2


def test_normalizing_keeps_near_silence_out() -> None:
    spec = np.full((200, b2.DEFAULT.n_bands), -100.0)
    spec[::7, ::5] += 1  # local maxima, but all far below the absolute floor
    assert len(b2.robust_peaks(spec, replace(NO_CAP, norm_frames=43)).t) == 0


def test_region_cap_limits_each_slice_of_the_bands() -> None:
    spec = music_like_spec()
    spec[:, :39] += 30  # the lowest sixth of the bands much louder
    p = replace(NO_CAP, peak_regions=6, region_cap=10)
    capped = b2.robust_peaks(spec, p)
    plain = b2.robust_peaks(spec, NO_CAP)
    blocks = capped.t // p.block_frames
    low = capped.f * 6 // p.n_bands == 0
    assert max(np.bincount(blocks[low])) <= 10
    assert max(np.bincount(blocks)) <= p.peaks_per_block
    assert (plain.f < 39).sum() > (capped.f < 39).sum()


def test_default_keeps_at_most_5_peaks_per_sixth_of_the_bands() -> None:
    p = b2.DEFAULT
    spec = music_like_spec()
    spec[:, :39] += 30  # the lowest sixth much louder
    pk = b2.robust_peaks(spec, p)
    group = (pk.t // p.block_frames) * 6 + pk.f * 6 // p.n_bands
    assert max(np.bincount(group)) == 5
    assert len(pk.t) > 0


def test_one_region_is_the_fingerprint_before_d53() -> None:
    from prototype.fingerprint import find_peaks

    x = np.random.default_rng(1).standard_normal(SAMPLE_RATE * 12)
    pk = find_peaks(b2.spectrogram(x, NO_CAP), NO_CAP)
    a, b = b2.fingerprint(x, NO_CAP), b2.triplets(pk.t, pk.f, NO_CAP)
    np.testing.assert_array_equal(a.hash, b.hash)
    np.testing.assert_array_equal(a.t, b.t)
    assert not np.array_equal(b2.fingerprint(x).hash, a.hash)


def test_dereverb_removes_a_tail_decaying_at_its_reverberation_time() -> None:
    p = replace(b2.DEFAULT, dereverb_rt60=0.5)
    frame_s = p.hop / SAMPLE_RATE
    spec = np.full((60, b2.DEFAULT.n_bands), -40.0)
    t = np.arange(40)
    spec[10:50, 30] = 40 - 60 * t * frame_s / 0.5  # an onset, then its reverberant tail
    out = b2.dereverb(spec, p)
    assert out[10, 30] == pytest.approx(40.0)  # the onset has nothing before it to predict it
    assert np.all(out[13:50, 30] <= spec[13:50, 30] - 9.9)  # the tail drops to the floor
    assert np.allclose(b2.dereverb(spec, b2.DEFAULT), spec)


def test_dereverb_applies_to_queries_only() -> None:
    x = np.random.default_rng(2).standard_normal(SAMPLE_RATE * 12)
    p = replace(b2.DEFAULT, dereverb_rt60=0.6)
    np.testing.assert_array_equal(b2.fingerprint(x, p).hash, b2.fingerprint(x).hash)
    assert not np.array_equal(b2.fingerprint(x, p, query=True).hash, b2.fingerprint(x).hash)


def test_onset_rank_keeps_onsets_and_drops_tail_ripples() -> None:
    spec = np.full((330, b2.DEFAULT.n_bands), -20.0)
    onsets = (20, 120, 220)
    for o in onsets:
        tail = 30 - 0.3 * np.arange(80)
        tail[9::9] += 3  # ripples in the tail: local maxima of their own
        spec[o : o + 80, 50] = tail
    # Over the last 8 frames an onset rises 50 dB; a ripple in a tail 0.6 dB.
    onset = b2.robust_peaks(spec, replace(NO_CAP, onset_frames=8))
    rise = dict(zip(onset.t.tolist(), onset.mag.tolist(), strict=True))
    assert all(rise[o] > 40 for o in onsets)
    assert all(m < 1 for t, m in rise.items() if t not in onsets)
    # With one peak per block, each block holding an onset keeps the onset.
    one = b2.robust_peaks(spec, replace(NO_CAP, onset_frames=8, peaks_per_block=1))
    blocks = dict(zip((one.t // NO_CAP.block_frames).tolist(), one.t.tolist(), strict=True))
    assert all(blocks[o // NO_CAP.block_frames] == o for o in onsets)


def test_previous_max_looks_only_backwards() -> None:
    x = np.array([[1.0], [5.0], [2.0], [0.0], [0.0]])
    np.testing.assert_array_equal(b2._previous_max(x, 2)[:, 0], [1.0, 1.0, 5.0, 5.0, 2.0])
