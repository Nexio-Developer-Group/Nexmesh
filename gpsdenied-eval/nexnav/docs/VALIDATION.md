# What was run and what it showed

All runs: CPU only (4 cores), synthetic NIDAR-like flight (gimbal-stabilised nadir camera, 200 m, 153 s), reference map = the imagery the video was rendered from, **no Jetson, no GPU**. Run folders are in `examples/` (CSV, summaries, resolved config; no imagery).

| Run | Command (from `gpsdenied-eval/`) | Result |
|---|---|---|
| S6 alone, replay 1 Hz (153 frames) | `nexnav run -c nexnav/configs/replay_nidar.yaml --engines s6` | 153/153 fixes, median 0.42 m, p95 1.06 m, max 1.55 m against the reference position |
| S5 alone, replay, map area limited to the flight area | `... --engines s5 --set map.roi=[18.51551,73.85166,18.52441,73.86079]` | 148/153 fixes (96.7 %), median 2.41 m, p95 9.78 m, max 16.6 m (evaluation: 2.46 m) |
| **Both together**, same area | `... --engines both --set map.roi=[...]` | S6 100 %, median 0.41 m; S5 96.7 %, median 2.41 m; fused: 293 of 306 rows `agree` (both within 30 m, averaged), 13 `single`; fused median 1.04 m, p95 4.81 m, max 9.9 m; no conflict |
| S5 on the whole 2 x 2 km map (same particles) | `... --engines both` without `map.roi` | **S5 converged on a wrong place**: 103 fixes accepted, median error 1,100 m. The fusion flagged all of them `conflict` and kept S6 (fused rows follow S6). Lesson below |
| Live path against ArduCopter SITL | `python nexnav/tests/sitl_live_check.py ...` | MAVLink telemetry read (attitude, height, GPS_RAW); S6 fixed 84 of 85 frames, median 0.48 m against the SITL GPS; after the SITL GPS was cut the real GPS showed fix type 1 while the autopilot's second GPS showed our injected fix (type 3, `GPS2_RAW`) and the EKF raised no failsafe for 60 s (without injection it failed in about 8 s in the evaluation). Injection stopped when nexnav stopped and the EKF then drifted (19 m in 6 s): do not stop the process in flight |
| Unit tests | `pytest nexnav/tests` | 8 pass: config validation, geometry, fusion modes (including carrying a fix forward in time), correction sign, an end-to-end replay with a dummy engine |

## Lessons that are now in the code or the docs
* **S5 depends on the search area.** The same 20,000 particles that converge over a 0.9 x 0.9 km area converged on a wrong place over 2 x 2 km. Limit the map (`map.roi`) to the area you will fly, or raise `engines.s5.n_particles` roughly in proportion to the area, and use the fusion mode so a wrong S5 cannot override S6 (`agree_or_best` keeps the primary on a conflict).
* **Relative paths are made absolute** at load time: the SWA-PF code changes the working directory while it runs, which broke other threads reading relative paths (found and fixed during the first both-engines run).
* **Retrieval scale:** MegaLoc fails with too small an input; every tile and the matching query crop are shown to the network at the same size (`engines.s6.ret_tile_px`), which also keeps retrieval consistent between altitudes (found when the 100 m clip crashed the first version).
* **Two engines finish at different times**, so the fuser carries the older fix forward to the newer one's time with the velocity of its frame before comparing; without it, a 2-second lag looked like a 24 m disagreement.
* **numpy < 2 for S5** (the SWA-PF code overflows on numpy 2).

## Not validated
Jetson hardware and CUDA paths; real camera drivers (CSI/GStreamer were not exercised: replay folders were); real flights; maps of another date than the camera; blur/haze at altitudes other than 200 m; S5 above 200 m (see `ALTITUDE.md`); injection into an autopilot other than ArduCopter SITL.
