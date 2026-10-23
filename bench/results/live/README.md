# Live monitoring, real time (D38-D40)

`python -m bench.scripts.live realtime` runs, scored by `eval/live_score.py` (`result.json` in each directory).

- `rt_cap16`, `rt_cap24`, `rt_cap32`, `rt_cap48`, `rt_kill16`: valid runs (AC power, battery not charging, no stall). `rt_cap16` and `rt_cap32` are reruns.
- `rt_cap16.invalid`: the battery began topping up from 99% during the run. `rt_cap32.invalid`: the machine went on battery during the run. A second 16-station attempt (also on battery) was replaced by the valid rerun. Not cited.
- `rt_cap48_d44`, `rt_cap56_d44`: valid runs at 12e1cae, after D44 (one FFT for both query filterbanks).
- `rt_cap16_d45`: valid run at a48a504, after D45 (live sources rejoined after a drop): checks that ending stations log what they did before.
- `rt_cap64.overloaded`: 64 stations; the worker fell so far behind that only 6 of 64 streams reached their end within the harness's limit (the playlists' length plus 180 s), so no result was scored.
