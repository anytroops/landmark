# Live monitoring, real time (D38-D40)

`python -m bench.scripts.live realtime` runs, scored by `eval/live_score.py` (`result.json` in each directory).

- `rt_cap16`, `rt_cap24`, `rt_cap32`, `rt_cap48`, `rt_kill16`: valid runs (AC power, battery not charging, no stall). `rt_cap16` and `rt_cap32` are reruns.
- `rt_cap16.invalid`: the battery began topping up from 99% during the run. `rt_cap32.invalid`: the machine went on battery during the run. A second 16-station attempt (also on battery) was replaced by the valid rerun. Not cited.
- `rt_cap48_d44`, `rt_cap56_d44`: valid runs at 12e1cae, after D44 (one FFT for both query filterbanks).
- `rt_cap16_d45`: valid run at a48a504, after D45 (live sources rejoined after a drop): checks that ending stations log what they did before.
- `rt_kill16_d45`: valid run at a4c7288 (D45 and its fencing fix): the worker kill repeated on the current code.
- `rt_cap64.overloaded`: 64 stations; the worker fell so far behind that only 6 of 64 streams reached their end within the harness's limit (the playlists' length plus 180 s), so no result was scored.
- `rt_cap48_d48`, `rt_cap64_d48`, `rt_cap80_d48`, `rt_kill16_d48`: valid runs at 1c9d98a, after D48 (the shared front end).
- `rt_cap64_d49`, `rt_cap80_d49`: valid runs at 1d2cf3c, after D49, with the in-flight cap at 16. `rt_cap96_d49`: same, not valid (the worker stopped reading audio for up to 12 s).
- `rt_cap64_d49_f32`, `rt_cap80_d49_f0`, `rt_cap80_d49_f24`, `rt_cap80_d49_f32`: valid runs at 9d1bbde, with the cap the harness sets (32, none, 24, 32). `rt_cap88_d49_f32`: valid run at ebdf59f (cap 32, the new default). `rt_cap96_d49_f32`: same, not valid (gaps of up to 105 s).
- `rt_cap88_d50`, `rt_cap96_d50`: valid runs at e16ac50, after D50 (late live windows dropped). `rt_cap112_d50`: same, not valid (gaps of up to 17 s).
- `diag_d50/`: diagnostic runs at b6f0d1e, not timed results: a 30 s CPU profile of the worker 200 s into 88- and 96-station runs (`worker_cpu_*.pprof`, top functions in `.txt`), and the machine's CPU and memory lines from `top` every 5 s over the same span (`machine_*.txt`).
- `rt_cap88_d51`, `rt_cap96_d51`, `rt_cap104_d51`, `rt_cap112_d51`: valid runs at 78de07e, after D51 (fingerprinting buffers kept between windows), each with `machine.txt`: the machine's CPU and memory lines from `top` every 30 s during the run.
