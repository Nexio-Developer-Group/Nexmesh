### 2.4 Test status matrix (every runbook test ID has a result or a recorded reason)

"done" = run and logged under `results/`; frame counts are in the per-solution chapters. "serial" = re-run alone on the idle machine (section 3.5).

| Solution | Done | Not run, with reason |
|---|---|---|
| S1 | T1 global search (1 Hz, 153 frames), T2 tracking (1 Hz and full 5 Hz), T3 real-time (serial), T4 constrained (ONNX export attempts, 1-core replay), T5 gate off, T6 no hints (weak: 77 frames, all on the first leg), T8 SITL (SIM-0..7, 3 seeds), T9 failure injection (first 60 s) | T1 at 5 Hz (about 7 h CPU: 1 Hz used); T7 fallback datasets (UAV-VisLoc is a 17.7 GB archive; MAFS/MUN-FRL unreachable); the repo's own visual-odometry gap filling was excluded from the adapter |
| S2 | T1 VIO only (substitute), T2 retrieval only, T3 fused causal, T4 align/ekf/causal, T5 consensus off, T6 corridor on/off, T7 tile size 60/120/240 m (1 Hz), T8 real-time and constrained (serial), T10 SITL (fused and VIO-only rows) | T9 MUN-FRL (unreachable, no mirror); OpenVINS itself (built, Docker image export out of disk); GPU run |
| S3 | T1 as published, T2 verified vs fallback, T3 no scale normalisation, T4 retrieval depth 1/5/10, T5 terrain, T6 real-time and constrained (serial), T8 our Kalman extension, T9 SITL (as published and with de-rotation) | T7 fallback datasets and UAV-VisLoc subset 06 (17.7 GB archive, MAFS unreachable) |
| S4 | none | T1 PX4 SITL demo (6.9 GB gisnav image pull: no space left on device; PX4 + Gazebo + QGC build; GPU, X11, 50 GB required), T2, T3, T6, T7 (depend on the stack), T4 not applicable, T5 dataset unreachable, T6 SITL: no estimates exist |
| S5 | T2 NIDAR replay (substitute semantic map), T3 particle sweep 1k/5k/20k/40k, T4 initialisation (3 seeds, 40 frames), T5 map resolution, T6 scene breakdown, T7 constrained (serial; 2,000 particles, 2 cores), T9 SITL (1 Hz fixes) | T1 MAFS reproduction and T8 cross-area (MAFS only on Baidu Pan, 3 attempts for a mirror) |
| S6 | T1 offline (5 Hz), T2 retriever swap, T3 matcher swap, T4 offline with tracking and real-time (serial), T5 PyTorch/ONNX variants (CPU), T7 SITL | T5 TensorRT (no GPU: `pip install tensorrt` fails; LighterGlue ONNX export fails with an IndexError), T6 fallback dataset (unreachable); every Jetson number |
| S7 | YFS90 documentation review, Bearing-UAV (weights run on 153 synthetic frames), STHN (not applicable: thermal only) | YFS90 code does not exist; STHN dataset not downloaded |
| SITL scenarios | SIM-0..SIM-7 (SIM-4 as 4a/4b), 3 seeds, rows S1, S2 (+VIO-only), S3 (+de-rotation extension), S5, S6, plus an ideal-fix control | S4 (no estimates); SIM-6 is a cold start at the start of cruise, not before take-off |
