# Decision memo: GPS-denied navigation solutions (one page)

*Synthetic NIDAR-like data, CPU only, same-map imagery: relative evidence only. Full report: `report/REPORT.md` / `REPORT.pdf`.*

**Ranking (owner weights 30/20/20/15/15):** 1. S6 our composite (Jetson baseline) 8.29; 2. S5 SWA-PF 7.78; 3. S3 tile-matching (Shuhaieva) 4.53; 4. S1 koksalan/gnss-denied-nav 4.25; 5. S2 uav-geolocalization (CAEVL + VIO) 2.38; 6. S4 hmakelin/gisnav 0.79. The winner is unchanged under every +10-point weight change.

**Best for each purpose**

| Purpose | Best | Basis |
|---|---|---|
| Highest accuracy (lowest accepted-fix median, fix rate over 20 %) | S3 | common 1 Hz set, synthetic NIDAR-like |
| Highest coverage at 50 m (recall@50) | S5 | recall over all frames |
| Lowest latency under the constrained replay | S2 | 1-2 CPU cores, CPU only (no Jetson) |
| Best autopilot integration (SITL outcome score) | S6 | SIM-2..SIM-7, 3 seeds |
| Best for Jetson-class deployment (edge-fit score) | S2 | latency, throughput, memory; NOT measured on a Jetson |
| Most robust over feature-poor terrain and weather | S5 | precision, blur/haze windows, worst terrain class |
| Easiest to adopt (setup effort) | S5 | minutes + 10 per failed step |


**Recommendation.** Build the NIDAR baseline on the **retrieve → match → PnP → consistency gate → tracking** structure that S6 implements, with S6's permissive-licence parts (MegaLoc, XFeat, OpenCV; confirm the LighterGlue weight licence), fed by the compass and barometer hints. On the synthetic data it was the only complete pipeline: 92 % of frames fixed, 0.4 m median, 100 % precision, a 100 % mission-completion rate in SIM-2, SIM-4 and SIM-7, and a 0.9 s step on a 2-thread CPU. **Treat this as a design direction, not a verdict:** (1) S6 is our own composite, written and tuned (gate thresholds, tile size, crop) on the very frames it is scored on, with no held-out set; (2) video and map come from the same imagery, so the real appearance gap is untested; (3) nothing was measured on a Jetson, ONNX/TensorRT export of the matcher failed on CPU, and in the 5 Hz real-time replay it processed only 21 % of the frames on two threads and 13 % on one core; (4) it needs a heading and height hint, which were noise-free here. S5 (particle filter on semantic labels) was the second most accurate and the most robust to blur and haze (93.5 % fixed), but it needs an odometry source, converges only after about 10 s, takes several seconds per step on this CPU at 20,000 particles (the first step 37 s) and its labels here came from a substitute map, so it fits a GPU companion computer, not a CPU-only edge board. S1's gate and tracking ideas are already inside S6; S1 itself retrieved poorly with pretrained weights (22 % of frames, all in the first 44 s). S3 is a good reference for the plain two-stage idea but has no rotation handling and no gating; S2 (VIO plus map fixes) failed with our under-trained encoder and a VIO substitute, so it is neither supported nor refuted by this test; S4 could not be run. Whichever solution is chosen, the autopilot side needs a velocity or odometry aid: even ideal 5 m-noise fixes cannot bridge a 20 s gap with our position-only EKF configuration (SIM-3 failed for every row, including the control).

**Next three tests.** (1) S6 on real NIDAR video with a different-date map, thresholds frozen, held-out flight. (2) S6 on a Jetson Orin with TensorRT FP16 end to end at 5 Hz (solve the LighterGlue ONNX export first). (3) Closed-loop SITL with a Gazebo-rendered camera and a velocity/odometry aid in the EKF, S6 against S5 on feature-poor terrain with fix outages and outliers.

**Could not be tested:** The real NIDAR video and its telemetry (we used a synthetic NIDAR-like flight); any field or non-synthetic imagery with a seasonal or lighting gap to the map; the fallback datasets MAFS 200 m, MUN-FRL and ALTO (unreachable) and the UAV-VisLoc reproduction; any GPU run and any Jetson run (no Jetson number in this report is measured); TensorRT; OpenVINS inside S2 (built but not run: the Docker image export ran out of disk); S4 (GISNav) in any form: the stack could not be stood up here (6.9 GB image, PX4/Gazebo/QGC build, GPU and X11, 50 GB); S5 on the MAFS dataset and the cross-area test; the YFS90 item (no code exists) and the STHN item (thermal imagery only); a fully closed-loop SITL in which each solution's output is recomputed from the simulated vehicle's own camera view (we used Mode B with precomputed estimates); take-off with GPS already denied (SIM-6 cuts GPS at the start of cruise); roll/pitch/attitude errors in the hints (they are noise-free).
