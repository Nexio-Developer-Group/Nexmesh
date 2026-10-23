# STATUS

| Solution | P0 | P1 | P2 | P3 | P4 | P5 | P6 | P7 | P8 |
|---|---|---|---|---|---|---|---|---|---|
| harness | - | - | in progress | | | | | | |
| S1..S7 | | | | | | | | | |

## Log
- 2026-10-22 P0 intake: folders created. NIDAR video NOT found (searched data/nidar, session uploads, filesystem for "*nidar*"). Branch 3 of section 3: NIDAR lines will read "not run: video not supplied".
- 2026-10-22 P0 env: 4 CPU, 15 GB RAM, ~30 GB disk, NO GPU, no Jetson, Docker CLI present. Reachable: github (public), pypi, pytorch CPU wheels, huggingface. Unreachable: pan.baidu.com (MAFS download). Constrained replay will be an approximation.
- 2026-10-22 P2 harness: metrics.py + run_replay.py + dummy adapter; 5/5 unit tests pass (pytest, /tmp/hv venv). TODO: sitl_bridge.py, plots.py, freeze.
- 2026-10-23 P1: no NIDAR video -> synthesising: SITL A>B>C>D @200m AGL (tools/sitl_mission.py), Esri z19 mosaic (benchmark-only, not committed), tools/render_video.py (1280x800 gray, global shutter, 60fps, blur+haze). SITL build in progress.
- 2026-10-23 P1 DONE (synthetic NIDAR): SITL ArduCopter 4.5.7 flew A>B>C>D at 200 m AGL (cruise 125.3-278.1 s sim time, 152.8 s, 9167 frames). Rendered over Esri z19 (0.283 m/px, benchmark-only). Camera 1280x800 (1.02 MP) grayscale global shutter 60 fps, HFOV 60 deg, GSD 0.18 m/px. Weather: blur at 21-31 s and 95-104 s, haze at 49-69 s, 95-107 s, 125-134 s (data/nidar/weather.json). 5 Hz frames (764) + truth.csv + imu.csv (SITL RAW_IMU) + camera.json. DEVIATION: this data is SYNTHETIC; the map used for rendering is the same map used as reference, so accuracy is optimistic (no seasonal/imagery gap). All NIDAR rows must be labelled "synthetic NIDAR-like".
- 2026-10-23 P3 S3: ALIKED weights 403 via github.com/raw -> worked around via raw.githubusercontent.com clone of Shiaoming/ALIKED (cached in ~/.cache/torch/hub). S1,S2,S6 delegated to background agents.
- 2026-10-23 P5 s2: HARNESS BUG run_replay.run sorts frames lexicographically (10.000 before 2.000) -> temporal adapters break; S2 uses own driver with numeric sort (solutions/s2_uav_geolocalization/run_s2.py)
- 2026-10-23 P3 S3 smoke (50 frames, 1 per 3 s, no hints): fix_rate 24%, median 115 m, p95 725 m; index build 652 s (CPU shared with 3 agents + SITL); latency p50 18.7 s UNDER LOAD (not valid). Frames rotate with heading, S3 has no de-rotation -> expected weakness; running S3-T1b with compass de-rotation as our labelled extension.
- 2026-10-23 P5 s4: GISNav NOT run: gisnav image 6.94GB compressed (ENOSPC on pull), PX4/Gazebo build + GPU/X11 needed; T1-T7 not run; static ANALYSIS/SETUP delivered (solutions/s4_gisnav, results/s4)
