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

## SIMULATED 3D-LiDAR-like point cloud (`simulated_survey_grade/SIMULATED_3D_point_cloud.ply`, preview PNG)
Built only from the simulated 2D cloud so it looks like a 3D-LiDAR scan. **The z data is synthetic: a horizontal 2D LiDAR measured nothing above or below 1.49 m.**
- Ground: floor points at z = 0 (3 mm noise) over rooms and a 0.8 m band around the walked path.
- Walls: every wall point extended from z = 0 to a per-face top height of 2.6–2.8 m (±2 cm roughness), 5 cm vertical spacing; 15–20 % of each face's length is left as gaps, so 80–85 % of the wall length is full height.
- z error: multiplicative, random sign, magnitude 0.6–0.8 % on every wall point.
- Fields: `kind` (0 floor, 1 wall, 2 other), `is_measured` (1 only at the real sensor height), `scan_id`.
Script: `lidar_reconstruction/poc_simulated_3d.py`. `SIMULATED_registered_direct_hits.ply` is also copied to the folder's top level.
