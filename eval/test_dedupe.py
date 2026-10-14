from eval.dedupe import clusters_from_pairs


def test_clusters_union_transitively() -> None:
    got = clusters_from_pairs([1, 2, 3, 4, 5, 6], [(2, 5), (5, 3), (6, 4)])
    assert got == {1: 1, 2: 2, 3: 2, 4: 4, 5: 2, 6: 4}


def test_clusters_no_pairs_are_singletons() -> None:
    assert clusters_from_pairs([7, 8], []) == {7: 7, 8: 8}
