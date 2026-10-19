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

## SIMULATED 3D point cloud (`simulated_survey_grade/SIMULATED_3D_point_cloud.ply`, preview PNG)
Built only from the simulated 2D cloud. Points on simulated wall faces are extended vertically from the floor (z=0) to the Blender roof height (3.025 m) in 0.1 m steps (assumes vertical walls); other points stay at the real sensor height (1.49 m). A ±0.8 % random multiplicative error is applied to every z.
Field `is_measured`: 1 = at sensor height (the only height a horizontal LiDAR sees), 0 = extrapolated. **Everything above/below sensor height is assumed, not measured.** Script: `lidar_reconstruction/poc_simulated_3d.py`.
`SIMULATED_registered_direct_hits.ply` is also copied to this folder's top level.
