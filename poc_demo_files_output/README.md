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
Derived from the real/simulated 2D data and the REAL sensor trajectory. **The z data is synthetic: the 2D LiDAR measured nothing above or below 1.49 m.**
- A virtual 64-beam 3D LiDAR (elevation -30..+30°) re-observes each wall return from the real sensor position at its real horizontal range: z = 1.49 + r·tan(elevation), vertical planar walls assumed. Near walls therefore get a shorter height span than far walls, as with a real spinning LiDAR.
- No ground and no ceiling points (a 2D LiDAR never sees the floor; beams that would hit floor/ceiling are dropped). Wall tops per face 2.6–2.8 m; 30 % beam dropout, 5 mm range noise → ~84 % of wall cells covered.
- z error: multiplicative, random sign, 0.6–0.8 % on every wall point.
- Fields: `kind` (1 wall, 2 other), `is_measured` (1 = beam at the real horizontal plane / non-wall returns), `scan_id`.
Script: `lidar_reconstruction/poc_simulated_3d.py`. `SIMULATED_registered_direct_hits.ply` is also copied to this folder's top level.
