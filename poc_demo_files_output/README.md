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
- No ground and no ceiling points. Wall tops per face 2.6–2.8 m; 22 % beam dropout, 5 mm range noise → ~84 % of wall cells covered.
- z error: multiplicative, random sign, 0.6–0.8 % on every wall point.
- Fields: `kind` (1 wall, 2 other), `is_measured` (1 = beam at the real capture plane / non-wall returns), `scan_id`.
Scripts: `lidar_reconstruction/lrf_height.py`, `poc_simulated_3d.py`.
