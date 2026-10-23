# S1 analysis: koksalan/gnss-denied-nav on synthetic NIDAR-like data

All numbers below come from runs under `results/s1/nidar/<run-id>/` (harness `metrics.summarize`, same metric code as the other solutions). Data: **synthetic NIDAR-like** nadir render (gimbal-stabilised, 200 m AGL, 12 m/s, 1280x800 gray, 0.18 m/px), reference map Esri z19 0.283 m/px. **The video was rendered from the same map, so the appearance gap is optimistic.** Weights are the **pretrained (not fine-tuned) DINOv2-base: every S1 result is DEGRADED** (the author's fine-tuned weights are not published). CPU only, 2 threads, box shared with other agents (latencies are inflated and noisy; the lead's alone-run numbers supersede them). Query keypoints limited to 1024 (repo default 2048) to make CPU runs feasible.

## 1. How it works (from the code that ran)
`adapter.py` -> `gdnav.localizer.Localizer.localize` per frame:
1. **Prior geometry**: the frame is rotated north-up by the compass yaw (`query.query_warp`, `heading_sign * yaw + offset`) and scaled to a fixed ground size from barometer height / focal length; a circular mask of 140 m diameter (inscribed circle of our frame) is cut. Without heading and height the method cannot start (it assumes north-up and known scale).
2. **Retrieval** (global mode only): DINOv2-base (CLS + GeM pooled patch tokens, 1536-d, L2-normalised) of the 224 px north-up patch; dot product against the 1225 pre-embedded map tiles (50 m stride); top-10 tiles. (Exact search done by a torch matmul; FAISS is not used in the localizer.)
3. **Fine matching**: SuperPoint keypoints of the warped query (up to 2048; we used 1024) are matched with LightGlue against a 450 m window of keypoints precomputed for the whole map (`MapFeatures`, kept in RAM), for each candidate; RANSAC fits a similarity transform (rejects rotation >15 deg or scale change >25 %), the candidate with most inliers wins.
4. **Pose**: inlier matches go to a homography RANSAC (4 px) and planar PnP (IPPE + LM refinement) on the ground plane: camera position (lat/lon), height above ground and off-nadir tilt.
5. **Confidence**: fix is trusted with >= 25 inliers.
6. **Consistency gate** (`sim/visual_gps.py::consistency_gate`, copied verbatim into the adapter): needs >= 50 inliers (>= 80 when no fix accepted for 5 s, "degraded"), |PnP height - baro| <= 8 m, |PnP tilt - IMU tilt| <= 4 deg (tilt skipped in degraded mode).
7. **Tracking mode**: once a fix is accepted, the next frame skips retrieval and matches only one 600 m window around the previous fix (`track_window_m`); a gate rejection keeps the prior, a "genuinely lost" frame (< 25 inliers) clears it and the next frame searches globally again.
8. Not in the adapter (out of scope): visual odometry between fixes (LK optical flow, `odometry.py`), ArduPilot GPS_INPUT bridge (`sim/visual_gps.py`), learned confidence model, detection/planning. The repo's "Lucas-Kanade fills gaps" part is therefore **not evaluated**; between fixes our adapter reports no position.

## 2. Data-flow stages (for the diagram)
camera frame (gray->RGB) + compass yaw + baro height -> **north-up/scale warp (OpenCV affine, circular mask)** -> **DINOv2-base embedding (PyTorch, HF transformers)** -> **tile-DB top-10 (dot product; DB built offline from map tiles, 1225 x 1536 float)** -> **SuperPoint (LightGlue pkg) on query** -> **LightGlue match vs precomputed map keypoints window (x10 candidates, or x1 in tracking)** -> **RANSAC similarity (OpenCV)** -> **homography RANSAC + planar PnP IPPE/LM (OpenCV)** -> **consistency gate (baro height, IMU tilt, inlier count)** -> accepted fix lat/lon/height -> (repo, not run here) EKF3 via MAVLink GPS_INPUT. Tracking feedback: accepted fix -> prior window for next frame.

## 3. Results (synthetic NIDAR-like, nadir)
Fix rate counts accepted fixes over ALL frames; precision = accepted fixes within 50 m (all accepted fixes were within 8 m in every run, so precision is 100 % and false fixes = 0 everywhere).

| Test | run id | frames | accepted | fix rate | median err | p95 | max | false fixes >50 m | latency p50 (loaded CPU) |
|---|---|---|---|---|---|---|---|---|---|
| S1-T1 global search, no prior, 1 Hz subsample | S1-T1-20261023-s0 | 153 (stride 5) | 34 | 22.2 % | 1.40 m | 7.05 m | 7.6 m | 0 | 34.0 s |
| S1-T2 tracking after first lock | S1-T2-20261023-s0 | 153 | 45 | 29.4 % | 1.50 m | 5.06 m | 5.9 m | 0 | track 9.7 s / global 18.4 s |
| S1-T2full tracking, full 5 Hz (**SITL file**) | S1-T2full-20261023-s0 | 764 | 221 | 28.9 % | 1.56 m | 4.01 m | 7.4 m | 0 | track 5.4 s / global 9.1 s |
| S1-T5 tracking, gate OFF | S1-T5-20261023-s0 | 153 | 46 | 30.1 % | 1.53 m | 5.43 m | 5.9 m | 0 | - |
| S1-T6a no baro, no heading (nominal 200 m, heading 0) | S1-T6a-20261023-s0 | 77 (stride 10) | 16 | 20.8 % | 1.66 m | 4.30 m | 6.6 m | 0 | 12.3 s |
| S1-T6b baro kept, heading removed | S1-T6b-20261023-s0 | 77 | 16 | 20.8 % | 1.69 m | 4.46 m | 5.3 m | 0 | 10.2 s |
| S1-T9 failure injection, early windows (tracking) | S1-T9-20261023-s0 | 61 (t<=60 s, 3 dropped by the injector) | 26 | 42.6 % | 1.34 m | 4.87 m | 5.9 m | 0 | 11.0 s |
| S1-T9late injection after lock is lost anyway | S1-T9late-20261023-s0 | 153 | 35 | 22.9 % | 1.30 m | 3.67 m | 5.9 m | 0 | 13.2 s |

Notes on each test:
* **T1 (global, 1 Hz)**: the headline: **only 22 % of frames get a fix**, all inside the first 44 s (leg A->B, plus a few frames). Median 1.4 m on the fixes it makes (optimistic: same map). Time to first fix 0 s. T1 at 5 Hz (764 frames x ~35 s each) was not run: ~7 h of CPU. As agreed in the brief we used 1 Hz; **the 5 Hz estimates.csv for SITL is `S1-T2full` (tracking enabled, 5 Hz), not a no-prior run**. In tracking mode the lead should expect position only until t = 44.2 s; afterwards S1 outputs nothing (accepted = False) for the rest of the flight.
* **T2 tracking**: a tracking frame costs about half of a global frame (5.4 vs 9.1 s on the quieter box, 9.7 vs 18.4 s in the loaded run): no DINOv2, one LightGlue call instead of ten. Fix rate rises 22 -> 29 % (the 1 s gaps in haze are not bridged; lock is lost at t = 45 s and never regained).
* **T5 gate off**: no change in false fixes (0 -> 0) because there were no wrong matches to reject; the gate rejected one correct fix (t = 45 s, 33 inliers < 50, degraded rule 80). On this data the gate is neutral to slightly harmful; its value (author: rejected all 16 fixes > 50 m off) cannot be shown because the failure mode here is "no match", not "wrong match". Inlier counts of correct fixes are 127-172, far above the 25 vs 50 thresholds, so the 25-inlier rule alone already separates them.
* **T6 no hints**: fix rate 16/77 (20.8 %) for both variants versus 19/77 (24.7 %) for T1 at the same even-second frames (3 fewer fixes, error unchanged). Because the baseline only localises on the first leg where the true heading is ~0 deg (north) and the height is the nominal 200 m, **the test cannot show the dependence on hints**: it only shows the method tolerates heading errors < 15 deg and height errors of a few metres, and it silently depends on those hints elsewhere (heading error > 15 deg is rejected by the similarity gate). Without the barometer the height/tilt part of the gate is not available (only the inlier count).
* **T9 failure injection** (tracking mode, 1 Hz; windows 3-9 s wrong heading +90 deg, 12-20 s 50 % random frame drop, 33-38 s low light (gain 0.12, gamma 1.8, noise), 40-45 s synthetic Gaussian blur sigma 8 px; video blur 21-31 s, haze 49-69 s):
  * wrong heading +90 deg: lock lost at the first corrupted frame (t = 3 s), no false fix accepted (rotation > 15 deg is rejected); global search re-acquired at the first correct frame (t = 9 s): **recovery time 0-1 frame (1 s)**.
  * 50 % frame drop: **no effect**: the last fix stays as prior and tracking resumed at the next frame (recovery 0 s; fixes 12, 16...).
  * low light: lock lost at once (t = 33), no fix during the 5 s window, re-acquired globally at t = 38 (first clean frame): recovery 1 s after the window.
  * blur sigma 8: lock lost (t = 40), at t = 45 a correct but weak fix (37 inliers, 2.9 m) was rejected by the degraded gate (needs 80) and lock was never regained before the natural haze (49 s): **no recovery within 15 s** (here it is the retrieval, see below).
  * natural blur 21-31 s: T2 (tracking) got fixes on 9 of 18 frames inside the video blur windows, T1 (global) on 2 of 18 (all haze frames: 0 of 41 in both).
  * In T9late (injections at 35-45 s etc.) lock was lost at the blur (35 s) and never regained.
* **T3 real-time, T4 constrained**: implemented, not run (lead runs alone). **T4 export attempt** (single process CPU, 2 threads, loaded box; results/s1/nidar/S1-T4-20261023-s0/t4.json):
  * DINOv2-base retriever: torch fp32 944 ms -> ONNX Runtime fp32 602 ms (max abs diff 4e-7, 346 MB) -> ORT dynamic INT8 214 ms (90 MB) **but INT8 embeddings cosine to fp32 only 0.58: unusable** (the author uses "sensitivity-aware" INT8, not reproduced; not tried).
  * SuperPoint: only the dense CNN exported (800x1280 input): torch 2.59 s -> ORT 1.44 s; NMS/top-k/descriptor sampling remain python. No TensorRT / Jetson path (author states none).
  * LightGlue: TorchScript ONNX export fails twice (`IndexError: list index out of range` in torch.onnx symbolic helper, opset 16 and 17, with adaptive depth/width disabled); `torch.onnx.export(dynamo=True, opset 18)` works (after installing onnxscript), 46.6 MB, fixed depth (no early exit): torch 2.40 s -> ORT 2.12 s for 512 x 1024 keypoints (lightglue_retry.json). Adaptive pruning is lost in the export.
  * Overall, one tracking frame ~ SuperPoint 3 s + LightGlue 2-3 s on 2 CPU threads: ~6 s per fix at best, ~1/30 of the 5 Hz frame rate; real-time is not reachable on this CPU without a large model/resolution cut.
* **T7 fallback datasets**: not run. Attempt: GitHub UAV-VisLoc readme reachable (links to Baidu, blocked); HF search found mirrors `haiduonghuynhle/UAV_VisLoc_dataset` (one 17.7 GB zip, AGPL tag) and `Demyd/UAV_VisLoc_dataset` (empty), `VERYBC/XIAN_Visloc` (not checked): too large for the disk allowance, so nothing fetched. A single flight could perhaps be range-extracted from the zip; not attempted.
* **T8 SITL**: lead.

Plausibility checks (results/s1/alignment_check.txt): shifting the truth by +-0.1 s makes the error worse (1.56 -> 1.8-1.9 m), so time alignment is right; mean bias N 0.01 m, E -0.66 m; fixes come from image matching (PnP height 196.7 m vs baro 197.1 m), truth is only used by the driver for hints (baro, yaw) and by `metrics.py`. The 1-2 m error is credible for same-map rendering with 0.5 m/px matching; real-imagery appearance gap will make it much worse (the author's own median is 24 m).

## 4. Failure analysis (frames in results/s1/figures/failures/t<T>.png: north-up query | expected tile at truth | top-1 retrieved tile; stats in failures.json)
| t (s) | condition | what happened | cause |
|---|---|---|---|
| 25 | video blur 21-31 s (peak 3 px) | no fix in global mode; the true tile has retrieval rank 23/1225, top-10 closest tile 311 m away | blur removes texture -> DINOv2 descriptor drifts; retrieval miss |
| 52 | haze 49-69 s | no fix; true tile rank 15 (top-10 nearest 162 m) | haze/low contrast; retrieval miss |
| 60 | haze | true tile IS rank 1 (8 m) but 0 inliers | haze contrast kills SuperPoint keypoints/LightGlue matches |
| 75 | clear air, forest/river | true tile rank 1 (12 m), 0 inliers | vegetation/water texture, gray query vs colour map -> few repeatable keypoints (see t75.png) |
| 100 | blur 95-104 + haze 95-107 | true tile rank 212 (nearest top-10 53 m) | combined degradation; retrieval miss |
| 130 | haze 125-134 | true tile rank 11 (just outside top-10), top-1 887 m away | haze; the fine-tuned retriever is exactly what is missing |
| 145 | clear | true tile rank 4 (26 m) but 0 inliers | matching failure on low-texture terrain (second half of the route) |
Summary of causes: (a) pretrained retrieval misses the right tile in the 10 candidates (blur/haze, ranks 11-212); (b) even with the right tile at rank 1, the SuperPoint+LightGlue stage finds 0-6 inliers on forest/water/haze; (c) after the lock is lost at t = 45 s the system never regained it in the remaining 108 s. By terrain: 0 accepted fixes after t = 44.2 s over ~108 s / ~1.2 km of path.
Frames with < 25 inliers in the gate counted as "genuinely lost" and cleared the tracking prior, so one haze frame forces an expensive global search that itself usually fails (no gap bridging; the repo's LK odometry would be needed).

## 5. Comparison with the author's claims
| claim (README) | ours | verdict |
|---|---|---|
| median 24 m, 97 % within 100 m on held-out real UAV-VisLoc flights, whole-map search without prior (fine-tuned DINOv2) | median 1.4 m on the fixes made, but only 22 % of frames get a fix (global); the README's own *pretrained DINOv2-B + LightGlue* row is 30 m median / 69 % / 75 % (fix rate numbers) | **not reproduced / partly consistent**: our weights are the author's "pretrained" baseline, which the author also reports as much weaker (top-1 retrieval 18 %). Our accuracy is optimistic (same map); our coverage is much lower than 69 % because of blur/haze, a 140 m footprint (the author's 250 m patch has more context) and gray-vs-colour imagery |
| 6.3 m median error with GNSS jammed (SITL, Gazebo, EKF3 fusion), 840 m without | not run by us (SITL by the lead) | not tested |
| VO halves worst-case error (86 -> 32 m) | VO not run (adapter excludes it) | not tested |
| consistency gate rejected all fixes > 50 m off | no wrong fixes occurred, gate neutral/slightly harmful (1 correct fix rejected) | cannot be confirmed on our data |
| fast path: 24 fixes/s | 0.1-0.2 fixes/s on 2 CPU threads (GPU claimed) | not reproducible on CPU; ~100-200x slower |
| no Jetson/TensorRT | confirmed; ONNX export works for DINOv2 and (dynamo) LightGlue, SuperPoint partial; dynamic INT8 breaks the descriptor | consistent |
Differences: synthetic data, nadir stabilised 200 m, different camera (FOV 60 deg), pretrained weights, CPU.

## 6. Verdict
* Strengths: very good accuracy when a fix exists (1-2 m here; PnP gives height and tilt that can be cross-checked with baro/IMU, a nice sanity gate); drift-free absolute fixes; clean modular code, matches its documentation; tracking mode halves the cost; heading and frame-drop faults are handled gracefully (no false fixes at all in any test); extensive training/evaluation scripts.
* Weaknesses: with off-the-shelf DINOv2 only ~1 frame in 4 gets a fix and nothing after t = 44 s; heavy dependency on compass heading (+-15 deg) and baro height; no published weights (cannot be used as delivered); heavy compute (needs GPU; 9-35 s per frame on 2 CPU threads); nothing between fixes without the VO part; thresholds tuned on one flight; SuperPoint weights are non-commercial; requires EPSG:4326 GeoTIFF.
* Use when: a GPU-equipped companion computer, a fine-tuned retriever for the operating area/season, reliable compass + barometer, fixed-wing/multicopter at moderate altitude over textured terrain, and a flight controller that accepts a second GPS. Not when: Jetson-class hardware without porting, low-texture terrain (forest, water, haze), no compass, or the weights cannot be trained.
* Risks observed: lock loss is silent and permanent in haze (no recovery for 108 s); gate rule "degraded" demands 80 inliers, which rejects good fixes after a gap; a wrong heading hint (>15 deg) blocks all fixes; prior gets cleared by any weak frame; memory ~2.4-3 GB; build 15 min on CPU for 1.9 km^2 (scales with area); harness-side: frame sorting by string.

## 7. Files
`adapter.py` (Adapter + MapShim + gate), `driver.py` (T1..T9 driver, injection, subsampling, realtime mode), `t4_constrained.py`, `t4_lightglue_retry.py`, `failure_figs.py`, `run_all.sh` (queue used), `camera.json`, `lock.txt`, `SETUP.md`. Oblique-camera calibration runs of the discarded first data set: results/s1/nidar_oblique/ (body-fixed 30-degree-oblique camera: stress case only, 12-frame calibrations: fix on 2/12 frames, errors 1.5-3.5 m).

## 8. Lead commands (run alone, serial, from /home/user/Nexmesh/gpsdenied-eval)
```
E=solutions/s1_gnss_denied_nav
# S1-T3 real time (5 Hz, 153 s wall, drops frames while busy)
OMP_NUM_THREADS=2 $E/env/bin/python $E/driver.py --id S1-T3 --mode realtime --config '{"query_kp":1024,"tracking":true}'
# S1-T4 constrained: ONNX variant timings (set threads / taskset for the constrained profile)
OMP_NUM_THREADS=2 taskset -c 0,1 $E/env/bin/python $E/t4_constrained.py --id S1-T4 --threads 2
# and constrained replay of the pipeline itself
OMP_NUM_THREADS=1 taskset -c 0 $E/env/bin/python $E/driver.py --id S1-T4rt --mode realtime --config '{"query_kp":1024,"tracking":true}'
```
SITL estimates: `results/s1/nidar/S1-T2full-20261023-s0/estimates.csv` (5 Hz, 764 rows; accepted only for t <= 44.2 s). The tile index cache must exist (`cache/`, ~135 MB, git-ignored, on this machine); first start otherwise takes 15 min.
