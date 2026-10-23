## 5. Findings and next steps

### Adopt
- **Retrieve → match → PnP → gate → track (S6 structure).** Evidence: S6-T1 (764 frames, 92.8 % fixed, 0.41 m median, 100 % precision), S6-T4 with tracking (95.3 % fixed, bridges the blur/haze gaps), SITL SIM-2/4/6/7 completed in all seeds. Retrieval quality is the lever: MegaLoc recall@1 84.3 % against 39.9 % for DINOv2-small CLS+mean-patch (S6-T2), and XFeat+LighterGlue loses only 8 points of fix rate against SuperPoint+LightGlue while running 4x faster (S6-T3).
- **A consistency gate with barometer and compass hints.** S1 and S6 produced no wrong fix above 8 m (S1) or none over 100 m (S6); S3 without any gate accepted 7 wrong fixes of 52 (precision@100 m 86.5 %), and S2's consensus step accepted 84 fixes of which 7 % were right (the wrong fixes agreed with each other).
- **Orienting the frame to the map with the compass.** De-rotating S3's frames raised its fix rate from 34 % to 56 % with no other change.

### Drop or do not rely on
- **S2 as tested.** Retrieval near chance (4.6 % recall@100 m) with our 50-minute CPU-trained CAEVL; fused RMSE 798 m against 5 m for the VIO alone. Fusion does help a drifting VIO if fixes are good (synthetic sanity test: 50-67 % lower RMSE with 20-40 % good fixes) but never helped here, so the author's 36 % claim is **not reproduced and not refuted** (different encoder, VIO, data).
- **S3 as a standalone localiser** (no rotation handling, no gate, 6-14 s per step on CPU).
- **S1 with pretrained weights.** The author's fine-tuned DINOv2 is not published; with DINOv2-base the true tile ranks 11-212 of 1,225 under blur and haze, and fixes exist only for the first 44 s.
- **S4** cannot be judged: the 6.9 GB image, PX4/Gazebo build, GPU and X11 requirements did not fit here. Its matcher needs a prior position and heading, so it cannot localise from a cold start, and ArduPilot support is marked unmaintained.

### Methodological findings
- **The video must be nadir.** Our first render used a body-fixed camera on a quadcopter that pitches −30° in cruise: every solution failed or needed rectification (S5 106.8 m median error without rectification; S6's nadir assumption failed completely). Any real NIDAR payload needs a gimbal or an attitude-aware rectification stage; S5's and S6's rectification extensions recover it only with accurate attitude.
- **Same-map imagery inflates accuracy.** Median errors of 0.07-0.4 m for S3 and S6 are a property of the test, not of the methods.
- **The EKF side matters as much as the vision side.** With a position-only second GPS, the EKF3 failsafe lands the vehicle about 68 s into the flight with no vision (SIM-1) and within about 8 s of the start of a 20 s fix drop-out (SIM-3 lands at about 88 s, even for ideal 5 m-noise fixes). 5 % outliers of 100-300 m (SIM-5) cost S5 and S6 two thirds of their missions: the fix quality gate needs to run on the autopilot side as well.
- **Latency.** On this CPU the steps cost 0.06 s (S2), 0.9 s (S6), 3-16 s (S5), 5.6 s (S3), 8.8 s (S1). Only S2 (poor fixes) and S6 (partly) approach a 5 Hz stream; the others need a GPU or a much lighter model before they can run at the frame rate.

### Next three tests (in order)
1. **S6 on real NIDAR video with a reference map of a different date**, with the thresholds frozen beforehand and a held-out flight. This removes the three optimism sources (same-map imagery, tuning on the test frames, noise-free hints).
2. **S6 on a Jetson Orin with TensorRT FP16**, measuring the whole pipeline (MegaLoc, XFeat, LighterGlue, PnP) at 5 Hz and its power mode; LighterGlue's ONNX export failed here and must be solved first.
3. **Closed-loop SITL (Gazebo-rendered camera) with a velocity/odometry aid in the EKF**, comparing S6 and S5 on feature-poor terrain (forest, water, fields) and with an injected fix outage and outliers, to fix the position-only EKF limits seen in SIM-3 and SIM-5.
