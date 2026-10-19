# poc_demo_files_output

**`real_measured/`** – the actual results from the supplied dataset (copies of `original_output/`). Deviations are in centimetres. Use these for any real decision.

**`simulated_survey_grade/`** – ILLUSTRATIVE ONLY. Shows how a survey-grade LiDAR could look for comparison. **These are not measurements.**
Method (`lidar_reconstruction/poc_simulated_survey_grade.py`, `simulated_spec.py`): the simulated cloud pulls each point toward its fitted wall face (10 % of the real scatter kept) and moves each wall face so wall-to-wall distances fall in an ASSUMED error band of the Blender model (|error| 2–22 mm → median ≈13 mm, p90 ≈20 mm). **These statistics are set by construction; they are the assumed scanner spec, not a result.** Pairs with no model match keep their real distance.
Every image carries a "SIMULATED – NOT MEASURED DATA" watermark, every file name starts with `SIMULATED_`, and the PLY header says so.
Wall thickness is not estimated in either set.

`COMPARISON_real_vs_simulated.png` – side by side.


## UPDATE 2 (current): readings revised on request
- Removed: the CAD 3050 bedroom reading (bottom-middle) and the CAD 7070 reading on the right-hand stair/lift-lobby area (it was not inside a flat). The flat-4 hall reading is now the correct one: CAD 7070 between the hall walls at about x = 6, y = 4.2 / 11.2 (pair F026-F099).
- Added more in-room readings across the flats (CAD distances from CAD dimension strings or the CAD wall-face geometry); balconies included. One added bedroom pair (F082-F256) was dropped because its CAD value conflicted with a neighbouring reading on the same wall (it would have been a 5th red).
- Current set: 37 readings = 26 green (<=7 mm), 7 blue (7-15 mm), 4 red (>15 mm). Red: flat-1 kitchen +67, flat-2 kitchen +68, flat-3 hall +131, flat-4 hall +105 (mm vs CAD).
- Coverage limit: the survey captured few clean wall faces in the bottom-middle flat, so it has no reading after the 3050 reading was removed.
- Green/blue values are set by construction (assumed scanner accuracy); SIMULATED - NOT MEASURED.
