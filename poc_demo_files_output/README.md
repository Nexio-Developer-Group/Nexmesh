# poc_demo_files_output

**`real_measured/`** – the actual results from the supplied dataset (copies of `original_output/`). Deviations are in centimetres. Use these for any real decision.

**`simulated_survey_grade/`** – ILLUSTRATIVE ONLY. Shows how a survey-grade LiDAR could look for comparison. **These are not measurements.**
Method (`lidar_reconstruction/poc_simulated_survey_grade.py`, `simulated_spec.py`): the simulated cloud pulls each point toward its fitted wall face (10 % of the real scatter kept) and moves each wall face so wall-to-wall distances fall in an ASSUMED error band of the Blender model (|error| 2–22 mm → median ≈13 mm, p90 ≈20 mm). **These statistics are set by construction; they are the assumed scanner spec, not a result.** Pairs with no model match keep their real distance.
Every image carries a "SIMULATED – NOT MEASURED DATA" watermark, every file name starts with `SIMULATED_`, and the PLY header says so.
Wall thickness is not estimated in either set.

`COMPARISON_real_vs_simulated.png` – side by side.

## Simulated distance statistics (final)
Assumed scanner spec (by construction, not a result): faces already within the band keep their real data; others are moved onto the Blender face plus a 6–10 mm assumed error.
Measured on the simulated cloud: median |Δ| ≈ 12 mm, p90 ≈ 24 mm, max ≈ 38 mm over 44 compared wall pairs (see `SIMULATED_wall_distances_vs_model_summary.json`).

## Real sensor height from the downward laser range finder (LRF)
`lrf.jsonl` (159,423 readings, ~50 Hz, same clock as the LiDAR) gives the real height of the sensor above the floor. It is bimodal: ~1.7–2.0 m most of the time and ~0.95 m in 62 episodes (median 13 s, nearly stationary), so the scan plane was NOT at a constant height. Heights per scan (median-filtered, interpolated): `real_measured/scan_height_lrf.csv`. Assumptions: LRF vertical, no vertical offset to the LiDAR plane, flat floor.
- `real_measured/registered_direct_hits_LRF_height.ply` – REAL points with z = real capture height (0.60–2.81 m).
- `simulated_survey_grade/SIMULATED_registered_direct_hits_LRF_height.ply` (also copied to the top level) – simulated xy (so every wall-distance rule/statistic is unchanged) with the same real capture heights.

## SIMULATED 3D-LiDAR-like point cloud (`simulated_survey_grade/SIMULATED_3D_point_cloud.ply`, preview PNG)
Derived from the simulated 2D data, the REAL sensor trajectory and the REAL LRF sensor height. **Everything above/below the capture height is synthetic: the 2D LiDAR measured only its own plane.** xy positions are untouched, so the wall-to-wall distance comparison (median ≈12 mm, p90 ≈24 mm) still holds.
- A virtual 64-beam 3D LiDAR (elevation -30..+30°) re-observes each wall return from the real sensor position, at its real horizontal range and real LRF height: z = height(t) + r·tan(elevation); vertical planar walls assumed.
- No ground and no ceiling points. Wall tops per face 2.6–2.8 m; 70 % beam dropout (3 cm column spacing), 5 mm range noise → ~84 % of wall cells covered.
- z error: multiplicative, random sign, 0.6–0.8 % on every wall point.
- Fields: `kind` (1 wall, 2 other), `is_measured` (1 = beam at the real capture plane / non-wall returns), `scan_id`.
Scripts: `lidar_reconstruction/lrf_height.py`, `poc_simulated_3d.py`.

## SIMULATED floor plan and 3D drawing from the simulated 3D cloud
- `simulated_survey_grade/SIMULATED_floorplan_from_3D.png / .pdf / .svg / .dxf`: plan cut at z = 1.0–1.6 m of the simulated 3D cloud; 256 wall faces (seeded from the earlier wall lines, each re-fitted from the slice points), 50 openings, 75 clear wall-to-wall distances in mm. Rooms are NOT filled: the simulated walls are fragmentary and rooms do not close without model door lines.
- `simulated_survey_grade/SIMULATED_3D_drawing.png`: the fitted wall faces extruded from the floor to 2.6–2.8 m (no ceiling, no floor slab).
- `SIMULATED_reachability_audit.json`: every point lies within range, within the virtual beam angles from the real sensor position and LRF height, between 0 and 2.87 m; no ground or ceiling points.
Scripts: `lidar_reconstruction/poc_plan_from_3d.py`. Open question: whether the 0.95 m LRF readings are real sensor lowering or the LRF hitting furniture.

## Wall comparison: Blender model vs real plan vs simulated plan (`COMPARISON_walls_real_vs_simulated_vs_model.png`, `COMPARISON_walls_summary.json`)
Per-item values: `real_measured/wall_distances_and_lengths_vs_model.csv`, `simulated_survey_grade/SIMULATED_wall_distances_and_lengths_vs_model.csv`. Script: `lidar_reconstruction/compare_walls_three_way.py`.
Wall length is compared only for faces seen end to end (both ends within 20 cm of a continuous model wall run, split at openings > 0.4 m); partial views are reported as coverage only.
The simulated distance statistics follow from the assumed scanner spec by construction; the simulated wall-length numbers were not tuned.

## Real CAD distances (`real_measured/real_cad_distance_btw_walls.dxf`, `.csv`; also in `original_output/`)
Source: the architect's CAD plan (PDF exported from ZWCAD, "B Wing Typ. Floor"). The CAD linework was registered to the Blender frame (scale 41.02 mm per PDF point, no rotation/mirror), so it overlays the LiDAR plan. DXF layers: `CAD_PLAN_WALLS` (plan drawing), `CAD_DIM_WALL_TO_WALL` + `CAD_DIM_TEXT` (only the 35 wall-to-wall distances used for the comparison; 20 confirmed by the CAD's own dimension strings, 15 measured on the CAD wall faces). Scripts: `cad_extract.py`, `cad_register.py`, `cad_distances.py`.

## Simulated distances DXF (`SIMULATED_3d_lidar_distance_btw_walls.dxf`, also in `simulated_survey_grade/` with a CSV)
All clear wall-to-wall distances computable from the simulated 3D cloud's plan cut (z 1.0–1.6 m): fitted wall faces + mm dimensions. SIMULATED – NOT MEASURED. Script: `lidar_reconstruction/poc_simulated_dxf.py`.
