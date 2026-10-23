# S4 GISNav - ANALYSIS (static; the stack was not run, see SETUP.md)

All statements below come from reading repo/ros/gisnav (commit f2c72641) and repo/docs, not from execution. No metrics exist for S4. Data would have been "synthetic NIDAR-like".

## How the matching works (plain language)
GISNav does not predict where the drone is by itself. It assumes the autopilot already supplies an approximate position, heading and altitude (MAVROS NavSatFix, pose, gimbal attitude). From that and the camera intrinsics, BBoxNode computes the ground footprint of the camera field of view (padded to a square) and GISNode asks the onboard WMS server (OWSLib GetMap, layers "imagery" and "dem", EPSG:4326, JPEG) for that area; it only re-fetches when the old map overlaps the new footprint by less than 85 percent. StereoNode converts the map to grayscale, rotates it by the vehicle heading (in 5-degree steps) so north-up map matches the camera orientation, and crops it to the camera image size, because the matcher is not rotation invariant. Query frame: TwistNode detects SIFT keypoints (RootSIFT descriptors). PoseNode detects SIFT on the map crop (cached while the map is unchanged), matches query and map descriptors with LightGlue (kornia LightGlueMatcher, SIFT variant, confidence filter 0.5), requires at least 15 matches, looks up per-point height from the DEM layer, and solves camera pose with cv2.solvePnPRansac (only 10 RANSAC iterations, no inlier check or reprojection-error gate that we could find). The resulting global pose is a noisy, discontinuous "absolute" measurement. In parallel TwistNode does visual odometry: SIFT + brute-force knn match between successive frames, PnP, scaled to metres using altitude; this is smooth but drifts. Two robot_localization EKFs fuse them (global EKF: absolute pose + differential VO). The fused odometry goes to UORBNode (px4_msgs SensorGps via micro-ROS/XRCE-DDS), or NMEA/UBX nodes, as a fake GPS fix (fix_type 3, fixed covariance values) so PX4 treats it as a GNSS source. Only the SIFT branch is used; the CPU path is designed to reduce keypoints (1024) and LightGlue layers.

## Architecture stages
1. Camera (gscam / ROS Image + CameraInfo) and autopilot state (MAVROS: NavSatFix, pose, gimbal attitude) - prior position/heading/altitude.
2. BBoxNode: FOV footprint on ground plane -> geographic_msgs/BoundingBox.
3. GISNode: WMS GetMap client (OWSLib, MapServer + PostGIS/GeoServer-style server) -> OrthoImage (RGB + 8-bit DEM).
4. StereoNode: grayscale, rotate by heading, crop to query size -> OrthoStereoImage.
5. TwistNode: OpenCV SIFT on query; BF-matcher VO between frames + PnP -> relative pose.
6. PoseNode: SIFT on reference, kornia LightGlue (PyTorch) matching, DEM lookup, cv2.solvePnPRansac -> absolute pose (ECEF via proj/affine from WMS CRS).
7. robot_localization ekf_local_node and ekf_global_node (5 Hz) fuse stage 5 and 6.
8. Extension nodes: UORBNode (px4_msgs SensorGps -> micro-ROS agent -> PX4 uXRCE-DDS), NMEANode, UBXNode.
9. Simulation environment (docker compose): PX4 SITL v1.14.2 + Gazebo Classic (San Carlos airport), QGroundControl, MapServer, PostGIS, micro-ROS agent.

## Results table
| Test | Status |
|---|---|
| S4-T1 PX4 SITL, GPS off | not run: 6.94 GB gisnav image pull hit ENOSPC; PX4/Gazebo/QGC build and GPU/X11 requirement; 3 attempts (SETUP.md) |
| S4-T2 NIDAR replay | not run (no stack) |
| S4-T3 two map resolutions | not run |
| S4-T4 real-time/constrained | not applicable (nothing to run); no command for lead |
| S4-T5 fallback dataset | not run: dataset unreachable |
| S4-T6 ArduPilot SITL via sitl_bridge | not run: no estimates.csv. Note ArduPilot service in repo is marked "unmaintained ... does not work" in docker-compose.yaml; mock GPS output only via NMEA/UBX/uORB so a bridge would have to be a custom node |
| S4-T7 failure injection | not run. Static expectations: WMS down -> GISNode keeps polling/connect timer, PoseNode gets no new reference (stale map used until timeout); wrong prior -> map from wrong place, SIFT matches fail (<15) or give false PnP (no outlier gate) |

No failure-window frames were produced (nothing executed). Weather windows from data/nidar/weather.json were not tested.

## Author claims vs reproduced
Project claims (README/landing page, no numeric claims): precise global position by comparing nadir frames to onboard GIS map; PX4 mission completes GNSS-free in SITL with `failure gps off`; simulation only, "do not use for real flights", "untested". Reproduced: nothing (not run). Demonstrated by project: only the Gazebo San Carlos airport demo video. Not reproduced because of resources, not because of a failure observed.

## Verdict
Strengths: MIT, modular ROS 2 design, standard OGC WMS map interface (any GIS server), EKF fusion of drift-free absolute fixes and VO, PX4 integration without touching the GPS driver. Weaknesses: research prototype, explicitly sim-only; heavy dependency stack (Docker, ROS 2 foxy, PX4 v1.14, micro-ROS, NVIDIA GPU recommended; 50 GB disk); relies on autopilot prior position/heading/altitude (cannot do cold-start global localisation, and must stay within the fetched map); matcher is not rotation invariant (needs heading); SIFT + LightGlue is sensitive to appearance gap (season, haze, blur) and our nadir 200 m scenes; flat PnP with 10 RANSAC iterations and no visible outlier rejection; fixed covariance; ArduPilot support unmaintained; single main maintainer; no published accuracy figures. Static concern: `self._device == "cpu"` in pose_node.py compares a torch.device to a str, which is probably always False, so the CPU-limited matcher config may never activate (unverified). Use it as a reference design for WMS-fed map matching; do not use for flights or as a drop-in for ArduPilot. Licence MIT (SETUP.md table).

## Risks observed
Disk/resource footprint (image set ~7 GB for gisnav alone), GPU dependency in compose files, no release/test evidence outside sim, mock GPS looks like real GNSS to the autopilot (fix_type 3, jamming/spoofing state unknown) so a wrong fix is trusted. Last release in earlier survey Aug 2024 (repo HEAD dated 2024-08-18).
