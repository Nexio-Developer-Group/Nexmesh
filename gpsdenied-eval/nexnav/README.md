# nexnav

GPS-denied absolute positioning for a drone with a downward camera and a reference map. Two engines from the evaluation:

| Engine | What it is | Needs | Good for |
|---|---|---|---|
| **s6** | retrieve (MegaLoc) -> match (XFeat + LighterGlue) -> planar PnP -> consistency gate -> tracking window | map, camera, heading and height | the default: 0.4 m median / 92 % of frames on the synthetic test, about 0.9 s per frame on 2 CPU threads |
| **s5** | SWA-PF: particle filter on semantic labels (SegFormer-B0) | map, camera, heading, height, **a velocity source** and the SWA-PF repository | feature-poor scenes, blur and haze (93.5 % of frames); slow without a GPU |

Run one, or **both together for redundancy** (`run.engines: [s6, s5]`); the two fixes are compared, averaged when they agree and flagged when they conflict.

> Evidence status: everything was measured on a **synthetic NIDAR-like flight, on a CPU**; 100-500 m only for S6 on short clips (`docs/ALTITUDE.md`). Nothing was measured on a Jetson or with a real camera. What was run and what it showed: `docs/VALIDATION.md`. S5 needs the map limited to the flight area (about 1 km across at 20,000 particles); see there. Test in SITL before letting it feed an autopilot.

## Install (Jetson)

```bash
bash scripts/install_jetson.sh        # system OpenCV, JetPack torch check, venv, nexnav, MegaLoc/XFeat/SWA-PF
nexnav config-template -o ~/nexnav.yaml     # every parameter, commented
nexnav check -c ~/nexnav.yaml               # map, camera, telemetry, dependencies
nexnav run   -c ~/nexnav.yaml               # live
```

Desktop / replay: `pip install -e ".[all,opencv]"`, then `nexnav setup` to clone the third-party code.

* Use the **system OpenCV** on a Jetson (`python3-opencv`): the pip wheel has no GStreamer, so CSI cameras would not open.
* Use the **JetPack PyTorch wheel** (CUDA). `engines.device: auto` uses CUDA when present; set `cpu` to force the CPU.
* S5 clones the SWA-PF repository (`nexnav setup --s5`); it has no licence, so its code is never bundled.

## What you configure (one YAML)

```yaml
run: {engines: [s6, s5], output_dir: ~/nexnav_runs}      # s6 | s5 | [s6, s5]
camera: {source: "csi:0", hfov_deg: 60, mount: nadir_stabilised}   # v4l2:/dev/video0 | csi:0 | gst:<pipeline> | rtsp://... | file:x.mp4 | folder:dir
map: {path: ~/maps/area.tif}                              # GeoTIFF, or PNG + JSON georeference (docs/MAPS.md)
telemetry:
  source: mavlink                                         # mavlink | csv | fixed
  mavlink: {connection: "serial:/dev/ttyTHS1:921600"}
  altitude: {source: relative_alt, offset_m: 0}           # relative_alt | rangefinder | vfr_hud | fixed
  gps_reference: gps_raw                                  # real GPS, only for the comparison logs
fusion: {mode: agree_or_best, primary: s6, agree_radius_m: 30}
```

Every parameter, its default and meaning: `docs/CONFIG.md` (generated from the same table the program validates against; unknown keys are rejected). Override anything on the command line: `nexnav run -c cfg.yaml --engines both --set engines.s6.tracking=false --max-seconds 120`.

Inputs read by the program:
* **Camera**: V4L2 / USB, Jetson CSI, any GStreamer pipeline, RTSP, a video file or an image folder (replay). Frames are undistorted if `camera.distortion` is given.
* **Telemetry over MAVLink** (serial, UDP or TCP): `ATTITUDE` (heading, roll, pitch), `GLOBAL_POSITION_INT` (height above home, EKF velocity), `GPS_RAW_INT` (the real GPS), `VFR_HUD`, `DISTANCE_SENSOR`. Or a CSV file for replay.

## Output: one folder per run

`runs/<timestamp>_<name>/` holds the resolved config, `system.json` (host, torch, CUDA, Jetson release), `run.log`, and

| File | Content |
|---|---|
| `estimates_s6.csv`, `estimates_s5.csv` | every processed frame: `lat, lon` (the absolute position the engine calculated), confidence, latency, **and the real GPS at that time with `err_north_m / err_east_m / err_m` and the correction (`corr_north_m, corr_east_m, corr_lat_deg, corr_lon_deg` = GPS minus estimate)** |
| `fused.csv` | the redundancy output with the same GPS comparison and `source` (s6, s5, s6+s5), `quality` (single / agree / conflict) |
| `frames.csv` | every camera frame: height, attitude, velocity used, which engines got it, GPS at that time |
| `telemetry.csv` | MAVLink telemetry at 10 Hz (live runs) |
| `summary.json`, `summary.md` | fix rate, median / p95 / max error against the GPS, mean correction, latency, dropped frames |

The error against the GPS is only meaningful while the GPS is trustworthy (it is the reference, not the truth).

## Optional: feed the autopilot

`output.mavlink_gps_input.enabled: true` sends the fused fix as MAVLink `GPS_INPUT` (a second GPS). Off by default. ArduPilot needs `GPS_TYPE2=14` and EKF3 position-only fusion (`EK3_SRC1_POSXY=3`, `EK3_SRC1_VELXY=0`); we validated this path only in SITL, and a 20 s gap in fixes made the EKF fail-safe even with perfect fixes, so keep an odometry or velocity aid in the loop.

## Tests

`pytest nexnav/tests` (config, geometry, fusion, correction sign, an end-to-end replay with a dummy engine). The real engines were run on the recorded flight and against ArduCopter SITL (`docs/VALIDATION.md`, outputs in `examples/`).
