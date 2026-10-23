# S6 "our composite" -- analysis

All results: synthetic NIDAR-like data (gimbal-stabilised nadir render, 200 m AGL, 12 m/s, 764 frames at 5 Hz). The reference map is the SAME Esri z19 mosaic the video was rendered from, so the appearance gap is zero and accuracy is optimistic. CPU only (Xeon 2.1 GHz, 2 threads, box shared with other agents, load avg 5-9): every latency here is "measured under shared CPU load" and untrustworthy; the lead re-runs. NO Jetson, GPU or TensorRT number was measured; any Jetson figure quoted is from the survey and unverified.
"Our composite" is a baseline we built; it is not an existing project.

## How it works (from the code that ran: s6lib.py, adapter.py)
Per frame, given hints (barometer height, compass yaw; roll/pitch = 0 for the nadir camera):
1. Rectify: ground-plane homography from attitude + baro height maps the 1280x800 frame to a north-up 1024x1024 canvas at map resolution (0.283 m/px; the camera GSD is 0.18 m/px so the frame is downsampled by 0.637; ratio recorded). Slight Gaussian blur before downsampling.
2. Retrieve: centre 384-px crop of the canvas, scaled 0.5 (192 px), MegaLoc descriptor (8448-d) -> FAISS IndexFlatIP over 625 map tiles (512 px, stride 256) -> top-5 tiles.
3. Match: XFeat (top 2048 keypoints, keypoints near the canvas black border dropped) vs XFeat features of a 1280-px map patch centred on the tile (cached) -> LighterGlue matches.
4. Geometry: USAC-MAGSAC homography RANSAC for inliers, then planar PnP (IPPE + LM refinement, camera K from camera.json, map pixels in metres on z=0) -> camera position (the fix), height, tilt, yaw.
5. Consistency gate: >=20 inliers, median reprojection residual <=3 px, |PnP height - baro| <= max(12 m, 10 %), tilt <= 50 deg, |PnP yaw - compass| <= 15 deg (tilt-vs-IMU check only when attitude is trusted; off here). First candidate that passes with >=70 inliers stops the loop, otherwise the best passing candidate is used; else accepted=False (never a guess).
6. Tracking window (T4 config `tracking=true`): after a lock, retrieval is skipped; a map patch is cut around the constant-velocity prediction and matched directly (single match, extra gate: jump <= 30 m/s*dt+40 m, >=15 inliers). 3 consecutive misses -> back to global retrieval.
Data-flow stages: frame -> [attitude/baro rectify, OpenCV] -> [MegaLoc ViT-B/14 DINOv2 backbone + SALAD aggregator, PyTorch] -> [FAISS flat IP] -> [XFeat, PyTorch] -> [LighterGlue, kornia] -> [MAGSAC homography, OpenCV] -> [IPPE planar PnP, OpenCV] -> [gate] -> [tracking state] -> lat/lon via map georef.
Structure follows hloc (retrieve-match-PnP); the gate/tracking ideas are from S1 reimplemented, no code copied.

## Setup facts
Tile index: 625 tiles x 8448 float32 = 21.1 MB; build 1088 s on shared CPU (1.7 s/tile, first run, 2 threads under load 9); cached afterwards. DINOv2-small index: 625 x 768 = 1.9 MB, 61 s. MegaLoc 228.6 M parameters (0.87 GB weights). Peak RSS ~2.1 GB (XFeat+LG) / 2.5 GB (SP+LG).

## Results (synthetic NIDAR-like, nadir; metrics from harness/metrics.py)
| Test | Run id | frames | fix rate | median err m | p95 m | max m | precision@100 m | note |
|---|---|---|---|---|---|---|---|---|
| S6-T1 offline, global retrieval every frame, 5 Hz | S6-T1-20261023-s0 | 764 | 92.8 % | 0.41 | 1.09 | 6.76 | 100 % | estimates.csv for SITL |
| S6-T4 (offline accuracy run with tracking window) | S6-T4off-20261023-s0 | 764 | 95.3 % | 0.35 | 1.01 | 5.36 | 100 % | 736 track / 28 global frames; real-time run NOT done (lead) |
| S6-T3 XFeat+LighterGlue, 1 Hz | S6-T3-xfeatlg-20261023-s0 | 153 | 92.2 % | 0.39 | 1.01 | 2.87 | 100 % | median inliers 270; latency p50 0.94 s |
| S6-T3 SuperPoint+LightGlue, 1 Hz | S6-T3-splg-20261023-s0 | 153 | 100 % | 0.14 | 0.42 | 2.50 | 100 % | median inliers 507; latency p50 3.7 s |
| S6-T2 MegaLoc (1 Hz) | S6-T2-megaloc-20261023-s0 | 153 | recall@1 84.3 %, @5 95.4 % | | | | | embed p50 273 ms |
| S6-T2 DINOv2-small CLS+mean-patch (1 Hz) | S6-T2-dinov2s-20261023-s0 | 153 | recall@1 39.9 %, @5 63.4 % | | | | | embed p50 59 ms |
| S6-T5 variants (CPU only) | S6-T5-20261023-s0 | | | | | | | see below |
T2 criterion: a tile (512 px) containing the true footprint centre. Time to first fix 0 s (first frame accepted). Full-run latency p50/p95 (shared load): T1 1.39/7.0 s per frame, T4off 1.77/8.2 s (the tracking path re-extracts the reference patch uncached each frame, so it is not faster on this CPU; the saving is the avoided retrieval and extra candidates). Not comparable to a Jetson.
Fix rate is mostly bounded by our own gate (see failures): of 55 rejected frames in T1, 39 had a raw (rejected) pose within 5 m of truth, 5 were wrong by >50 m (haze), so the gate trades fix rate for 100 % precision.

T3 caveat: SP+LG is more accurate and robust on this data but 4x slower on CPU, and the SuperPoint weights are research-licence only. T2 caveat: our DINOv2-small baseline is untrained for VPR (CLS + mean patch tokens); the gap to MegaLoc is partly that.

### T5 variants (CPU, 2 threads, shared load, single process; "measured under shared CPU load")
| Variant | p50 ms | note |
|---|---|---|
| MegaLoc PyTorch 196x196 | 265 | |
| MegaLoc ONNX Runtime CPU | 228 | cosine vs PyTorch 1.000 (random input), 915 MB onnx |
| XFeat backbone PyTorch 1024x1024 | 200 | |
| XFeat backbone ONNX Runtime CPU | 64 | max abs diff 1.2e-4 |
| XFeat full extract PyTorch 1280x800 | 228 | |
| LighterGlue PyTorch (2048 kpts) | 278 | |
| LighterGlue ONNX | export FAILED (torch.onnx IndexError in transpose; kornia LightGlue has data-dependent pruning) | 1 attempt on that exporter, no workaround tried |
| TensorRT FP16 (any) | NOT RUN: no GPU/driver; `pip install tensorrt` fails (3 attempts, see SETUP.md) | |
| Jetson Orin (any) | NOT MEASURED | survey claims 15 Hz MegaLoc, 30+ Hz XFeat+LighterGlue are unverified here |
Accuracy change of ONNX variants was checked on descriptor/feature outputs only, not end-to-end replay.

## Failure analysis (T1; figures: results/s6/figures/failures/*.png = frame | expected map crop at truth | map crop at the pipeline's rejected estimate)
Fix rate by condition (T1): clear 99.6 %, haze 90 %, blur-only 19.6 %, blur+haze 100 % (blur+haze windows overlap at 95-104 s where gaussian blur was probably weaker than in the pure blur window 21-31 s).
1. t=21.4-30 s (blur window peak sigma 3 px): ~40 frames rejected by the residual gate (median residual > 3 px) although 200-280 inliers and raw error < 1 m (t=21.4: raw err 0.4 m). Cause: our gate threshold, not the geometry -> a loose gate would give ~+5 % fix rate; kept as run (no post-hoc tuning).
2. t=30.2 (end of blur): 24 inliers, raw error 23.6 m, correctly rejected (few inliers + residual).
3. t=52.4 (haze 49-69 s, strength 0.6): retrieval returned wrong tiles, 6 inliers, raw error 986 m, rejected by 4 gate checks. Haze lowers contrast so top-5 tiles missed.
4. t=56.0 and 57.2 (haze): same pattern, raw errors 1044 m / 1049 m, rejected (inliers 6).
5. t=55.0 (haze): 17 inliers, 20 m raw error, rejected (few_inliers).
Also 57.4 s: 7 inliers, 175 m. The tracking run bridges these windows (95.3 % fix, no wrong accepted fix) because the constant-velocity window supplies the location prior; global retrieval is what fails in haze.
No accepted fix exceeded 6.8 m error in T1 (precision@100 m = 100 %); accepted-fix precision is the strong point, fix availability under blur/haze the weak one.

## Comparison with claims
- Survey "MegaLoc ~15 Hz and XFeat+LighterGlue 30+ Hz on Jetson Orin": NOT reproduced and not testable (no Jetson). On this 2-thread shared CPU: MegaLoc 3.8 Hz alone (265 ms), XFeat extract 4.4 Hz, LighterGlue match 3.6 Hz; end-to-end frame p50 1.4 s. Hardware classes differ by orders of magnitude, so no inference about Orin is drawn.
- No author accuracy claim exists for the composite itself (it is our design).

## Verdict
Strengths: sub-metre median error on synthetic data, 100 % precision of accepted fixes, absolute (no drift), permissive licences for the main path (MIT/Apache; LighterGlue weight licence unconfirmed), modular (retriever/matcher swappable, ONNX path for XFeat/MegaLoc works). Weaknesses: heavy for CPU (MegaLoc 228 M params, 0.9 GB weights), global retrieval fails in haze, strict residual gate rejects blurred-but-correct fixes, needs height and heading (and tilt) hints, optimistic data (same map). Use: as an absolute-position corrector with an onboard prior (tracking mode) on GPU-class hardware; do not rely on global retrieval alone in low-contrast scenes or on CPU-only boards.
Known risks observed: (a) the first (oblique, body-fixed camera) dataset made the nadir assumption fail completely (north-up NCC 0.1-0.2); attitude had to be known to rectify (probe of 7 oblique frames with true attitude: 427-574 inliers, <0.5 m: results/s6/nidar_oblique/probe_7frames.txt, stress result "body-fixed 30-deg-oblique camera"); (b) hint yaw units (deg) and string-sorted frame names in the harness; (c) truth60.csv still carries the old -30 deg pitch; (d) T6 fallback dataset: not run (dataset unreachable, no open mirror attempted beyond the lead's note); (e) T7 SITL: not run by S6 (lead uses results/s6/nidar/S6-T1-20261023-s0/estimates.csv).

## Commands for the lead (run ALONE, one at a time, from solutions/s6_jetson_composite with `source env/bin/activate; export OMP_NUM_THREADS=2`)
- T4 real-time replay with tracking: `python driver.py --run-id S6-T4-rt-<date>-s0 --mode realtime --cfg '{"tracking":true}'`
- Constrained (2 cores): `taskset -c 0,1 python driver.py --run-id S6-T4-c2-<date>-s0 --mode realtime --cfg '{"tracking":true}'` (add `systemd-run`/cgroup memory limit as desired)
- Clean latency (alone): `python driver.py --run-id S6-T1lat-<date>-s0 --stride 5`
- T5: `python t5_variants.py --run-id S6-T5-<date>-s0`
Extra per-frame stage timings are in extras.csv of each run.
