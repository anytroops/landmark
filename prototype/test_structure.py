import numpy as np

from prototype import anlz, structure


def grid(bars: int, bpm: float = 120.0) -> list[anlz.Beat]:
    step = 60.0 / bpm
    return [anlz.Beat(i % 4 + 1, bpm, i * step) for i in range(4 * bars)]


def test_labels_are_per_bar_kinds_and_starts():
    beats = grid(32)
    phrases = [
        anlz.Phrase(b, k) for b, k in ((1, "intro"), (33, "up"), (65, "chorus"), (97, "outro"))
    ]
    st = anlz.Structure("high", 129, phrases)
    kind, start = structure.labels(st, beats)
    names = [structure.KINDS[k] for k in kind]
    assert names[:8] == ["intro"] * 8
    assert names[8:16] == ["build"] * 8
    assert names[16:24] == ["drop"] * 8
    assert names[24:] == ["outro"] * 8
    assert list(np.nonzero(start)[0]) == [0, 8, 16, 24]
    assert structure.drops(kind) == [16]


def test_drops_need_a_lead_in():
    k = {n: i for i, n in enumerate(structure.KINDS)}
    kind = np.array([k["intro"], k["drop"], k["breakdown"], k["build"], k["drop"], k["drop"]])
    assert structure.drops(kind) == [4]


def test_bar_features_follow_the_energy():
    fps = structure.FPS
    beats = grid(32)
    power = np.full((int(64 * fps) + 10, 234), 1e-6)
    loud = slice(int(32 * fps), int(48 * fps))  # bars 16 to 23
    power[loud] = 1e-2
    feats = structure.bar_features(power, structure.bars(beats, 0.0))
    assert feats.shape == (32, structure.N_FEATURES)
    energy = feats[:, :24].mean(axis=1)
    assert energy[16:24].min() > energy[:15].max() + 2
    assert feats[16, 24:48].mean() > 2  # the jump into the loud part


def test_r0_finds_a_phrase_boundary_on_the_eight_bar_grid():
    rng = np.random.default_rng(0)
    feats = rng.normal(0, 0.05, (48, structure.N_FEATURES))
    feats[16:32, :24] += 3.0  # a loud phrase
    kind, start = structure.baseline_r0(feats)
    assert start[0] == 1 and start[16] == 1 and start[32] == 1
    assert structure.KINDS[kind[20]] == "drop"
    assert structure.KINDS[kind[0]] in ("intro", "build")


def test_predictions_become_rekordbox_phrases():
    beats = grid(24)
    k = {n: i for i, n in enumerate(structure.KINDS)}
    kind = np.array([k["intro"]] * 8 + [k["build"]] * 8 + [k["drop"]] * 8)
    start = np.zeros(24, int)
    start[[0, 4]] = 1
    st = structure.to_structure(kind, start, beats)
    assert [(p.beat, p.kind) for p in st.phrases] == [(1, "intro"), (17, "intro"), (33, "up"),
                                                      (65, "chorus")]  # fmt: skip
