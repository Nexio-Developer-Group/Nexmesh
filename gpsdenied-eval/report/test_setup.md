### 2.1 Hardware and software

| Item | Value |
|---|---|
| Machine | cloud container, 4 CPU cores (Intel Xeon @ 2.1 GHz), 15 GB RAM, no GPU, no Jetson, about 30 GB disk allowance shared by everything |
| OS / Python | Ubuntu 24.04 container; Python 3.13 (3.9 / 3.11 / 3.12 per solution where the solution needed it); every solution in its own virtualenv (`solutions/<sol>/env`), versions in `solutions/<sol>/lock.txt` |
| Torch | CPU wheels only (torch 1.10 for S5, 2.x for the others) |
| Autopilot | ArduCopter 4.5.7 SITL built from source (`Copter-4.5.7`), run at 8x speed-up; pymavlink 2.4.50 |
| Repository commits | `solutions/<sol>/COMMIT.txt` (S1 572b9ff, S2 9983dca, S3 e63334e, S4 f2c7264, S5 e8a9217; S7 YFS90 968eaf8) |
| Random seeds | replay seed 0 for every solution; SITL seeds 0, 1, 2 (outlier injection and drops use the seed; SITL's own physics is deterministic, so seeds mainly matter for SIM-5) |

CPU timing caveat: while the solution agents were working, the machine was shared by up to six heavy jobs, so latencies in the per-solution chapters were measured under load. The latency table in section 3.5 comes from **serial re-runs on an otherwise idle machine** (commands in the appendix).

### 2.2 Inputs used and why (runbook section 3)

| Input | Used | Branch / reason |
|---|---|---|
| NIDAR video | **not supplied**: no file under `data/nidar/`, in the session uploads, or anywhere on disk (search for "nidar") | runbook branch 3. Per your instruction we **generated a synthetic NIDAR-like flight** instead; every NIDAR line in this report is therefore "synthetic NIDAR-like", not real NIDAR footage |
| Flight | ArduCopter SITL flew A→B→C→D at 200 m AGL, 12 m/s: A→B 500 m north, B→C 500 m east, C→D 250 m west and 400 m south; cruise 152.8 s, 1,464 m | truth lat/lon/yaw from SITL's true state at 60 Hz (`truth60.csv`), 5 Hz subset `truth.csv` |
| Telemetry | SITL IMU (`imu.csv`), barometer/attitude hints from truth | the solution hints are noise-free (a limit on realism) |
| Reference map | Esri World_Imagery, zoom 19 (0.283 m/px) over Pune, India (about 1.8 x 1.8 km); GeoTIFF `data/nidar/map.tif` (EPSG:3857) | benchmark use only; never redistributed (the map, frames and video are git-ignored; report figures contain no imagery) |
| Camera | gimbal-stabilised **nadir** pinhole, 1280x800 (1.02 MP) grayscale, global shutter (every frame is one instant, no rolling-shutter skew), 60 fps, 60° horizontal FOV, f = 1108.5 px, 0.18 m/px at 200 m AGL | frames rendered by warping the map through the exact ground-plane homography |
| Weather | minor Gaussian blur (σ up to 3 px) at 21-31 s and 95-104 s; close haze (transmission down to 0.4, low-frequency cloud pattern) at 49-69 s, 95-107 s and 125-134 s; sensor noise and vignetting | schedule in `data/nidar/weather.json`, windows marked on every error-over-time plot |
| DEM | not used by any solution that ran | S4 would have used one |
| Fallback datasets MAFS 200 m, MUN-FRL, ALTO | **not run**: MAFS only on Baidu Pan (unreachable from here, no open mirror found), MUN-FRL / ALTO links not reachable or too large; UAV-VisLoc exists as a 17.7 GB archive on Hugging Face, too large for the disk allowance | all "fallback dataset" test IDs read "not run: dataset unreachable" |

### 2.3 Deviations from the runbook (all of them)

1. **Synthetic video, not NIDAR** (above). The reference map is the very imagery the frames were rendered from, so there is no seasonal, lighting or resolution gap between query and map. This makes matching **much easier than in the field** and several solutions return implausibly small errors (for example S3 verified fixes at 0.07 m); we checked this against the metric code and the data alignment and it is genuine, a consequence of identical pixels.
2. **Data defect found and fixed during the run.** The first render used a body-fixed camera on a quadcopter that pitches −30° in cruise, so frames were strongly oblique (runbook: nadir or near-nadir). We re-rendered with a stabilised nadir camera and **re-ran every primary test**. The oblique results are kept in `results/<sol>/nidar_oblique/` and discussed as a stress result (S5: 106.8 m median error without rectification; S6: nadir assumption failed completely; S2: near chance).
3. **1 Hz subsampling.** Several tests ran on every 5th frame (1 Hz, 153 frames) because CPU time per frame is 5-35 s for S1, S3 and S5; each result names its frame count. The master comparison uses the **same 153 frames for every solution**.
4. **CPU only, no Jetson.** The constrained replay is an approximation: the process is pinned to 1-2 cores with `taskset`. No number in this report is a Jetson measurement.
5. **Degraded or substitute components**, each flagged where it appears: S1 uses pretrained DINOv2-base (the author's fine-tuned weights are unpublished); S2's VIO layer is our substitute (OpenVINS built but could not be run) and its CAEVL encoder is under-trained; S5's semantic map is our substitute from the repo's SegFormer.
6. **SITL is Mode B (video in the loop), not closed loop.** The vehicle flies the A→B→C→D mission in SITL; the solutions' estimates were precomputed offline on the recorded video. At each recorded frame position the bridge passes the solution's *error vector for that frame* applied to the SITL vehicle's true position as a `GPS_INPUT` fix. A solution's output is never recomputed from the SITL vehicle's own view, and its compute latency is not modelled except in SIM-4 (added 200 / 500 ms). SIM-6 (cold start) starts the GPS cut at the beginning of cruise instead of before take-off, because there are no ground-level frames.
7. **Fix feed rate** follows each solution's run: 5 Hz (S6, S2, S1 tracking run) or 1 Hz (S3, S5), without modelling the seconds of CPU time per fix.
8. **Not run** (reasons in the chapters): S4 (stack could not be stood up: 6.9 GB image pull ran out of disk, PX4/Gazebo/QGC needs a GPU, X11 and 50 GB), S1/S2/S3/S5/S6 fallback datasets, S2 with OpenVINS, TensorRT anywhere, GPU runs, Jetson runs, S7 items other than Bearing-UAV.
