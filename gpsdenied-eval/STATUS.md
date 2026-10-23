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
- 2026-10-23 S6: MegaLoc loaded (local clone of torch.hub repo, github zip API 400), pipeline works (sub-m error, 400-570 inl); TensorRT pip install fails (no nvidia-smi / disk) -> T1 full run started
- 2026-10-23 S7 paper_only: review done; Bearing-UAV run on synthetic NIDAR 28 m median vs 23 m prior baseline -> results/s7/bearing
- 2026-10-23 P5 s2: T1,T2,T3,T4,T5,T6,T7 done (fusion worse than VIO alone: undertrained CPU encoder ~2% recall@100); OpenVINS built but not run (ENOSPC); T8 implemented not run (results/s2)
- 2026-10-23 DATA DEFECT: original frames were body-fixed oblique (quad pitch -30 deg). Re-rendered gimbal-stabilised nadir (data/nidar); old set kept as data/nidar_oblique (stress). All solution agents told to re-run. Harness fixes: numeric frame sort; yaw hint documented in DEGREES.
- 2026-10-23 P5 s2: re-ran on nadir data (results/s2/nidar), old oblique moved to results/s2/nidar_oblique; fusion still worse than VIO alone; OpenVINS 2nd build ok but ENOSPC at export
- 2026-10-23 S5 DONE: nadir T2 median 2.46 m/rmse 3.53 m (substitute map, 1 Hz, 20k particles, 37 s first step); T3/T4/T5/T6 done; T1/T8 not run (MAFS unavailable); T7 implemented (solutions/s5_swa_pf/constrained_replay.sh); oblique stress in results/s5/nidar_oblique; see solutions/s5_swa_pf/ANALYSIS.md
