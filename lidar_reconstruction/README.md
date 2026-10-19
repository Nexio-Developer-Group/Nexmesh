# LiDAR trajectory reconstruction, Blender registration and floor-plan generation

Floor 18 "Typical Floor – Wing B": 52 min of handheld horizontal 2D LiDAR (35,410 scans, 6.77 M returns) → trajectory, registered point cloud,
Blender scene, dimensioned 2D floor plan, validation report. Everything below was **executed on the real data**; numbers come from `output/`.

## Result summary

| Item | Result |
|---|---|
| Sphere | one candidate: object `Sphere`, world (-7.2003, 9.9944, 1.4905) m, scale 0.29 (radius 0.29 m), no rotation, no parent. Blender unit = 1 m. |
| Meaning of sphere | treated as the sensor/person start position, no offset. Evidence: global localisation of the first 12 keyframes with **no positional prior** ranks (-7.30, 9.83) m first (score 0.904 vs 0.858 for the next, an identical-looking room 29 m away). First estimated pose is **0.19 m** from the sphere (sphere radius 0.29 m). |
| Start heading | not given by the sphere; estimated from data: **≈ 89°** (CCW from +X). Wall-direction ambiguity of 90° resolved by the unique global fit. |
| LiDAR data | genuine sequential scans (t0,t1,a[],r[]), 13.33 Hz, 192 median pts/scan, angles centi-deg, ranges mm, 52.0 min, no gaps >0.16 s, **no IMU/odometry**. |
| LiDAR-only SLAM | scan-to-submap odometry: median inlier 0.987, rms 2.6 cm; see `validation_report.json`; Manhattan heading prior + 43 verified loop closures. Residual drift remains: rigid best fit of this map to the model reaches only 22 % of points within 10 cm. |
| Model-anchored trajectory | 1,242 chunk anchors; **92.9 % of 6.7 M points within 10 cm and 84.8 % within 5 cm of the Blender section**, median 1.8 cm; similarity-fit scale = 1.00013. |
| Floor plan | 286 wall faces (LiDAR line fits), 86 wall-thickness pairs, 74 openings, **65 rooms** (51 ≥70 % perimeter observed, 14 partially). 85 % of the model's wall length is supported by LiDAR (within 8 cm). |
| Validation | 29 pass / 3 fail / 4 unverified (details below). |

## Outputs (`output/`)

* `trajectory_registered.csv` – every scan: time, x, y, z (assumed), yaw, σxy, σyaw, anchor inlier fraction. `trajectory_slam_only.csv` – LiDAR-only trajectory in its own SLAM frame.
* `registered_processed.ply` – 293,502 persistent points at 2 cm (x, y, z, scan_id, t). `raw_derived_registered.npz` – all 6.77 M returns, no filtering. `slam_only_unregistered.ply`.
* `registered_scene.blend` – copy of the original building plus collections `ORIGINAL_BUILDING`, `START_REFERENCE` (the sphere, untouched), `LIDAR_REGISTERED` (5 cm display points via a geometry-nodes modifier; radius exposed), `LIDAR_RAW` (LiDAR-only cloud, hidden), `TRAJECTORY` (curve + one pose marker per minute + keyframe points), `FLOOR_PLAN_GEOMETRY` (observed walls black / model-derived grey / openings orange / rooms coloured by observation status), `REGISTRATION_REFERENCES`, `DIAGNOSTICS`. Toggle visibility per collection. Full-resolution reload: `python reload_data.py output/registered_scene.blend`.
* `floorplan.svg / .pdf / .png / .dxf` – metric plan (grid, scale bar, +Y reference arrow, legend; DXF layers per status). `floorplan_geometry.json`, `measurements.csv` – every feature with coordinates, length/width, units, source, method, σ, status (`observed` / `partially_observed` / `model_derived`).
* `registration.json` (transforms, candidates, residuals), `validation_report.json`, `diagnostics/*.png`, `pipeline.log`, `inventory.json`.

## Coordinate systems and transforms

* **Blender world** (= building/model coordinates; no parents, no unapplied scale on the relevant objects): metres, right-handed, Z up. Everything in `output/` marked "registered" is in this frame.
* **Sensor frame**: `x = r cosθ, y = r sinθ`, `θ = angle_sign · a·0.01° + offset`. `angle_sign = -1` (clockwise-positive device). Mirroring was tested via global search: the non-mirrored interpretation fits the model; mirrored does not.
* **SLAM frame**: first pose = identity. `T0` (SLAM → Blender), yaw 87.0° at (-7.2725, 9.8315), matrix in `registration.json`. This is only the *initial* alignment; the final trajectory is the optimised anchored one (poses are not a rigid transform of the SLAM-frame ones).
* **Floor-plan frame**: identical to Blender world; origin (0,0) = Blender origin; +Y is the reference direction (no true-north data exists).
* z of the trajectory = sphere height 1.49 m, **assumed**; a horizontal 2D LiDAR cannot observe height, roll or pitch.

## Method (stages in `run_pipeline.py`)

1. `inspect_inputs`, `load_lidar`: inventory; raw files never modified.
2. `estimate_trajectory`: point-to-line scan-to-submap matching (Huber), constant-velocity prior, 0.3 m / 10° keyframes, correlative recovery (needed for 0.04 % of scans).
3. `pose_graph`, `graph_solver`: per-keyframe Manhattan wall-direction heading factors (robust, soft; the building is ~orthogonal, heading drift was ±5°), FFT-correlative loop-closure search with ICP verification (2,012 candidate pairs → 53 → 43 after consistency check), robust SE(2) pose graph. **No model information is used here.**
4. `register_to_blender`, `model_anchor`, `model_extract`: global start localisation against the Blender section at sensor height, then sequential propagation from the start, chunk-to-model matching (FFT + point-to-line), piecewise-rigid initialisation and alternating pose-graph solves with robust unary priors (σ 0.10 m, strong enough to remove drift; weak enough that local scan geometry is determined by the LiDAR). The sphere enters as a soft x,y prior (σ 0.25 m). Scale is not estimated (metric LiDAR, metric model); the similarity fit is only a check.
5. `build_map`: scan poses from keyframes, accumulation, persistence filter (cell visited by ≥4 scans spread over ≥3 scans), 2 cm voxel.
6. `extract_floorplan`, `measure_geometry`, `export_floorplan`: Hough wall-face detection (normals from 60 neighbours), TLS refit, merge, corner completion (recorded as inferred, ≤25 cm), back-to-back face pairing, openings, free-space rooms from observed barriers + doorway closures + closed door leaves + model-derived walls, outward correction of 7.5 cm (construction-derived bias of the 1-cell barrier dilation), labels and nominal sizes from the model text objects.
7. `blender_integration`, `diagnostics`, `validate_results`.

## Validation (see `validation_report.json` for all 36 checks)

Failed (kept as failures):
* **S9** pose-graph solver reports "max evaluations" (300) rather than convergence; cost decreases slowly but fit statistics do not change between rounds.
* **M2** p90 LiDAR-face offset to the nearest parallel model wall = 12.7 cm (median 2.5 cm): angled walls and as-built differences.
* **M3** wall thickness: LiDAR pair thickness differs from nominal by a median 8.4 cm (n=50). Face blur (point band ≈ ±3–4 cm) and thick walls wrongly paired inflate this; **do not use wall thickness from this plan**.

Unverified (data missing): sensor calibration/extrinsics (I5), IMU/odometry fusion (I6), as-built reference dimensions (M9), and R6 (diagnostic only).

## Limitations / unresolved ambiguities

* **Anchoring is model-dependent.** LiDAR-vs-model agreement (R4, R5, M1) is partly by construction and is not independent evidence of absolute accuracy. The LiDAR-only trajectory (`trajectory_slam_only.csv`) is the model-free result but has drift (≈ up to 2 m in the far half). Scan geometry is never altered, only keyframe poses are constrained.
* Room dimensions: median |measured – nominal label| 6.4 cm over 53 rooms, but the spread is large (σ ≈ 0.36–0.39 m) because some free-space regions merge or split rooms relative to the label (open living/dining areas, shafts, passages, a few leaks through unobserved doorways). Treat `status` and `observed_perimeter_frac` as the quality indicator; nominal sizes are design intent, not as-built truth.
* No independent as-built survey was available; recommended: tape/total-station checks on ~10 spanning dimensions.
* Sensor model, scan-time motion distortion (no deskew; ≤ ~7 cm at walking speed), glass/reflective returns and moving people are not modelled; persistence filtering handles transients conservatively.
* Only one floor was surveyed (single level; the model is a single floor). Floor transitions cannot be detected from a horizontal LiDAR; none were required.
* Open doors vs closed door leaves in the model change the apparent openings; openings are LiDAR-observed gaps, door leaves of the model are used only as room closures.

## Installation / execution

Python 3.13, Linux (tested), Blender as module `bpy` 5.2.2 LTS. `pip install -r requirements.txt` (Open3D was not usable: missing `libEGL`; SciPy implementations are used instead).
```
mkdir -p data && cp <Floor18_TypicalFloor_WingB.blend> data/ && curl -o data/lidar_h.jsonl <lidar URL>   # inputs are not committed
python run_pipeline.py --config config.yaml            # all stages, cached in output/; --stages a,b --force to rerun
python -m pytest -q tests                                # 10 tests: angle convention, transforms, matching, registration, PLY, filters, real-data tracking
```
Full run ≈ 20 min on 4 cores (odometry 90 s, SLAM 100 s, anchoring 5.5 min, rest < 2 min). Processing is deterministic (no random sampling).
