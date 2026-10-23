# Live monitoring, real time (D38-D40)

`python -m bench.scripts.live realtime` runs, scored by `eval/live_score.py` (`result.json` in each directory).

- `rt_cap24`, `rt_cap48`, `rt_kill16`: valid runs (AC power, battery not charging, no stall).
- `rt_cap16.invalid`: the battery began topping up from 99% during the run. `rt_cap16`: rerun, invalid again (the machine went on battery). `rt_cap32.invalid`: the machine went on battery during the run. Not cited; to be rerun.
- `rt_cap64.overloaded`: 64 stations; the worker fell so far behind that only 6 of 64 streams reached their end within the harness's limit (the playlists' length plus 180 s), so no result was scored.
