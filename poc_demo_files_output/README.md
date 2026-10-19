# poc_demo_files_output

**`real_measured/`** – the actual results from the supplied dataset (copies of `original_output/`). Deviations are in centimetres. Use these for any real decision.

**`simulated_survey_grade/`** – ILLUSTRATIVE ONLY. Shows how a survey-grade LiDAR could look for comparison. **These are not measurements.**
Method (`lidar_reconstruction/poc_simulated_survey_grade.py`, `simulated_spec.py`): the simulated cloud pulls each point toward its fitted wall face (10 % of the real scatter kept) and moves each wall face so wall-to-wall distances fall in an ASSUMED error band of the Blender model (|error| 2–22 mm → median ≈13 mm, p90 ≈20 mm). **These statistics are set by construction; they are the assumed scanner spec, not a result.** Pairs with no model match keep their real distance.
Every image carries a "SIMULATED – NOT MEASURED DATA" watermark, every file name starts with `SIMULATED_`, and the PLY header says so.
Wall thickness is not estimated in either set.

`COMPARISON_real_vs_simulated.png` – side by side.


## UPDATE: CAD-referenced simulated readings (current state of the simulated files)
The simulated point clouds (`SIMULATED_registered_direct_hits*.ply`, `SIMULATED_3D_point_cloud.ply`) and `SIMULATED_3d_lidar_distance_btw_walls.dxf/.csv` were rebuilt so the in-room wall-to-wall readings follow a chosen pattern against the real CAD distances.
- 29 in-room readings: 19 green (<=7 mm), 6 blue (7-15 mm), 4 red (>15 mm). Hallway readings (lobby / passage / lift shaft / corridor) were removed; 8 in-room readings were added (each with a CAD dimension string, except the flat-4 hall 7070 which is assumed by symmetry with flat 3).
- Red readings kept: flat-1 and flat-2 kitchens (CAD 2400, +66 / +68 mm), flat-3 hall (CAD 7070, +129 mm) and one same-type flat-4 hall reading (CAD 7070, +109 mm).
- Wall positions were moved so that the readings, measured back from the rebuilt 3D cloud, match the targets (closed loop; largest miss from target ~8 mm, median ~1 mm). The green/blue values are SET BY CONSTRUCTION (assumed scanner accuracy), not results.
- Older simulated-vs-Blender-model comparison files (distances/lengths) were removed because they described the previous simulated state. Scripts: `poc_cad_targets.py`, `poc_apply_targets.py`, `poc_targets_outputs.py`.
