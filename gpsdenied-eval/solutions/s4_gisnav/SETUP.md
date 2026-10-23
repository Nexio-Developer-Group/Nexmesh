# S4 GISNav - SETUP (result: stack NOT stood up; reason documented with evidence)

Commit: f2c72641ea04f959b0578caf0a3d36922cda2452 (2024-08-18, repo has a single squashed commit locally; README says 2,793 upstream commits).
Host: Intel Xeon @2.1 GHz, 4 cores shared, no GPU, Docker 29.9.1 + Compose v5.6.0, free disk 5.9 GB at start, 2.5 GB at end (shared with 5 other jobs; docker build cache already 8.1 GB from other jobs).
Wall-clock: about 25 min, 3 failed steps. Evidence: results/s4/nidar/S4-T1-20261023-s0/log.txt

## Documented route (README / docs deploy-with-docker-compose.md)
1. Install Docker Compose plugin + NVIDIA Container Toolkit (docs: "strongly recommended" NVIDIA GPU; compose file docker-compose.gpu.nvidia.yaml reserves `driver: nvidia`). Ubuntu 22.04 assumed, 16 GB RAM recommended, 50 GB free storage stated in system-requirements.md.
2. `git clone https://github.com/hmakelin/gisnav.git && cd gisnav && make install` (builds a .deb and installs the `gnc` wrapper).
3. `gnc build px4 gisnav --with-dependencies; gnc create px4 gisnav; gnc start px4 gisnav && gnc stop autoheal`
4. Upload ksql_airport_px4.plan with QGroundControl (GUI, needs X server), start mission, then in MAVLink console `failure gps off`; check `listener sensor_gps`.

## Attempts (all failed; none could reach step 3)
| # | What | Result |
|---|------|--------|
| 1 | `docker pull ghcr.io/hmakelin/gisnav:latest` (prebuilt gisnav service, avoids build) | Registry reachable (ghcr works; Docker Hub also pulled ubuntu:focal fine, no 429 seen this time). amd64 manifest: 24 layers, 6.94 GB compressed. Pull aborted: `failed to copy: failed to send write ... no space left on device`. Exceeds the lead's 4 GB cap. |
| 2 | Alternative source: build from repo Dockerfiles (ros:foxy base for mavros/gisnav, micro-ros-agent:foxy) | Base manifests resolvable on Docker Hub, but gisnav image alone is ~7 GB compressed (see 1); not built, disk. |
| 3 | `px4` service: Dockerfile clones PX4-Autopilot v1.14.2, runs Tools/setup/ubuntu.sh (Gazebo Classic, toolchain) and builds; plus qgc, mapserver, postgres, fileserver images | Not started: build cache is already 8.1 GB, free disk 2.5 GB, 4 shared cores; PX4+Gazebo build is multi-hour on this CPU. Also QGroundControl GUI + Gazebo need an X display (none) and the docs require an NVIDIA GPU. |
Also not done: `make install` (`gnc`), since all its consumers need the images.

Verdict wording: "does not work outside the sample setup because" the documented route requires a 50 GB / NVIDIA-GPU / X11 workstation (Docker image set is ~7 GB for the gisnav service alone, plus PX4/Gazebo/QGC/MapServer); on this 4-core CPU-only box with 2.5-6 GB free disk it cannot be built. This is a resource-limit result, not proof the software is broken.
Note also: the Docker GISNav services were never run here, so GISNav-specific failures (e.g. CPU fallback path) are unobserved. Static reading found a bug-like line in pose_node.py: `self._device == "cpu"` compares a torch.device to a string (always False in torch>=?; torch.device == str is False), so the CPU-limited SIFT/LightGlue branch (1024 keypoints, 5 layers) would likely never be selected and the GPU-sized matcher (9 layers, unlimited SIFT) would run on CPU. Unverified by execution.

## Test status
S4-T1 not run (above). S4-T2, T3, T6, T7 not run (depend on the stack; no estimates.csv exists, so no S4 SITL rows for the lead). S4-T4 not applicable. S4-T5 not run: dataset unreachable. adapter_ros2_recorder.py is an untested ROS 2 sketch (never executed). No map served, no WMS written (not needed without ROS stack; MapServer docker image would also cost disk).
Could be done with a pure-python port (SIFT 1024 + LightGlue + solvePnPRansac against map crops, needs torch+kornia ~1 GB): not done because of disk; that would be our reimplementation, not GISNav.

## Licences
| Component | Licence |
|---|---|
| gisnav (repo, ros pkgs) | MIT (c) 2022 Harri Makelin |
| PX4-Autopilot v1.14.2 | BSD-3-Clause |
| ROS 2 foxy, MAVROS, robot_localization, micro-ROS agent | Apache-2.0 / BSD / (as upstream; not checked here) |
| kornia (LightGlueMatcher) / LightGlue weights | Apache-2.0 / LightGlue weights licence (SuperPoint non-commercial; SIFT variant: check) - not verified |
| MapServer, QGroundControl | MIT-style / Apache-2.0+GPLv3 dual (not verified) |

## Documentation rubric (0-3)
- Install steps work as written: 1 (steps are clear but need NVIDIA GPU, X11, 50 GB, hour-long builds; could not be executed here; docs themselves say "will take a long time").
- Config explained: 2 (ROS params and compose overlays documented in docs and docstrings).
- Results reproducible: 1 (no benchmark, no numbers; sim only, warns against real use).
- Algorithm described: 1 (README and landing page do not; docs/system-architecture and module docstrings describe node graph; matching only from source).
