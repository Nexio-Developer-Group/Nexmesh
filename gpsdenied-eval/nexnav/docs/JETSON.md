# Running nexnav on a Jetson

**Status: not tested on a Jetson.** The evaluation machine had no GPU. The CUDA code paths (device selection, `engines.s6.fp16`) are written but unexercised, and every speed in this folder is a CPU number. Measure first, with `nexnav run` on a recorded flight (`--no-realtime`) and then live.

## Prepare the device
1. Flash JetPack 5.x or 6.x. Max performance while testing: `sudo nvpmodel -m 0 && sudo jetson_clocks`.
2. Install the JetPack PyTorch / torchvision wheels (the PyPI wheels have no CUDA for aarch64 Jetson). `bash scripts/install_jetson.sh` checks this and stops if torch is missing.
3. Keep the **system OpenCV** (`python3-opencv`): it has GStreamer, which CSI cameras need. The installer uses `--system-site-packages` for this reason.
4. SWA-PF (engine s5) needs `numpy<2`; JetPack's numpy is usually 1.x already.
5. `nexnav setup --prefetch` downloads MegaLoc / XFeat / SWA-PF and the MegaLoc weights while online. Flight computers are usually offline: do it on the bench.

## Wiring the autopilot (ArduPilot example)
* Companion UART to a telemetry port: `SERIALn_PROTOCOL = 2` (MAVLink2), `SERIALn_BAUD = 921` (921600). In the config: `telemetry.mavlink.connection: "serial:/dev/ttyTHS1:921600"`.
* nexnav only needs `ATTITUDE`, `GLOBAL_POSITION_INT`, `GPS_RAW_INT` (reference), optionally `DISTANCE_SENSOR`; it requests them at `stream_rate_hz`.
* To feed the position back (optional): `GPS_TYPE2 = 14`, `EK3_SRC1_POSXY = 3`, `EK3_SRC1_VELXY = 0`, `GPS_AUTO_SWITCH`/`GPS_PRIMARY` as in your failsafe plan, then `output.mavlink_gps_input.enabled: true`. **Test in SITL first** (the runner in this repository's `harness/sitl_runner.py` shows a working setup); even perfect fixes could not hold the EKF through a 20 s gap.

## Camera
* Calibrate (OpenCV checkerboard) and put `fx, fy, cx, cy` and `distortion` in the config; `hfov_deg` is a fallback. Global shutter and a fixed exposure that avoids blur matter more than resolution.
* `camera.mount`: use `nadir_stabilised` only with a gimbal that really holds the camera straight down. A camera rigid on a quadcopter pitches 20-30 degrees in cruise; set `body_fixed` so roll and pitch are used (S6 rectifies the image; S5 has an extension for it). Without either, every engine failed in the evaluation.
* Height: the autopilot's `relative_alt` is above home. Set `telemetry.altitude.offset_m` if the flight area is higher or lower than home, or use a rangefinder (`altitude.source: rangefinder`).

## Tuning for speed (all in the config)
| Lever | Effect |
|---|---|
| `engines.s6.kpts` 2048 -> 1024 | about 2x faster matching, fewer inliers |
| `engines.s6.topk` 5 -> 3 | fewer tiles verified per global search |
| `engines.s6.tracking: true` | after a lock only one match per frame (about half the cost of a global search) |
| `engines.s6.tile_scales_m` | fewer scales = smaller index; cover your altitude range only |
| `engines.s5.n_particles` | 20,000 was needed; 5,000 converged late and 1,000 locked on a wrong place |
| `engines.s5.label_res_m` 1.0 -> 2.0 | lighter label map, worse accuracy (3.4 -> 10.7 m median in the evaluation) |
| `run.realtime: true` | engines skip frames while busy, so latency stays bounded |
Memory in the evaluation (CPU): S6 about 2.1 GB, S5 about 2.3 GB resident; both together fit an 8 GB module but GPU memory was not measured.

## Run as a service (example)
```ini
# /etc/systemd/system/nexnav.service
[Unit]
Description=nexnav
After=network.target
[Service]
User=nvidia
ExecStart=/home/nvidia/.nexnav/venv/bin/nexnav run -c /home/nvidia/nexnav.yaml
Restart=on-failure
[Install]
WantedBy=multi-user.target
```
Every start creates a new run folder, so a restart never overwrites earlier logs. Check free disk space: `output.save_frames_every` stays 0 unless you want frames.

## Before the first flight
1. `nexnav check -c ~/nexnav.yaml` reports READY.
2. Replay a recorded flight (`--camera folder:... --telemetry-csv ... --no-realtime`) and read `summary.md`.
3. Ground test with the aircraft carried at working height; compare `estimates_*.csv` with the GPS columns.
4. SITL test of the autopilot side before enabling `mavlink_gps_input`.
