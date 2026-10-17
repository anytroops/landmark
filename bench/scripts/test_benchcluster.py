import pytest

from bench.scripts.benchcluster import parse_battery

AC = "Now drawing from 'AC Power'\n -InternalBattery-0 (id=1)\t"
BATT = "Now drawing from 'Battery Power'\n -InternalBattery-0 (id=1)\t"


@pytest.mark.parametrize(
    ("out", "want"),
    [
        (AC + "100%; charged; 0:00 remaining present: true", ("ac", 100, "charged")),
        (AC + "82%; charging; (no estimate) present: true", ("ac", 82, "charging")),
        (AC + "99%; finishing charge; 0:06 remaining", ("ac", 99, "finishing charge")),
        (AC + "80%; AC attached; not charging present: true", ("ac", 80, "AC attached")),
        (BATT + "95%; discharging; 5:00 remaining", ("battery", 95, "discharging")),
        ("Now drawing from 'AC Power'", ("ac", -1, "none")),
    ],
)
def test_parse_battery(out: str, want: tuple[str, int, str]) -> None:
    assert parse_battery(out) == want
