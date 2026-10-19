# poc_demo_files_output

**`real_measured/`** – the actual results from the supplied dataset (copies of `original_output/`). Deviations are in centimetres. Use these for any real decision.

**`simulated_survey_grade/`** – ILLUSTRATIVE ONLY. Shows how a survey-grade LiDAR could look for comparison. **These are not measurements.**
Method (see `lidar_reconstruction/poc_simulated_survey_grade.py`): real deviations are remapped  (<5 cm → 0–7 mm, 5–15 cm → 7–13 mm, >15 cm or unobserved → 15–18 mm), and the simulated point cloud pulls each point toward its fitted wall face, keeping 10 % of the real perpendicular scatter.
Every image carries a "SIMULATED – NOT MEASURED DATA" watermark, every file name starts with `SIMULATED_`, and the PLY header says so.
Wall thickness is not estimated in either set.

`COMPARISON_real_vs_simulated.png` – side by side.
