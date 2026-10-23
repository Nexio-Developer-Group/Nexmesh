# S3 tile-matching pipeline: analysis

All numbers: **synthetic NIDAR-like, nadir (stabilised) frames**, 1 Hz subset (153 frames, every 5th frame) unless stated; metrics from `harness/metrics.py`; run folders `results/s3/nidar/<run-id>/`. The query frames are warps of the same map the tiles are cut from, so appearance matches the map far better than in the field.

## How it works (from the code that ran: adapter.py, which mirrors the notebook cell by cell)
1. Offline: the map is resampled so one pixel equals the camera ground resolution, cut into 1024x1024 px tiles with stride 512 (361 tiles), and every tile is turned into a 384-d DINOv2-small embedding (518x518, class token, L2-normalised). Index build 700 s on a loaded CPU, 0.6 MB.
2. Per frame: the frame is embedded the same way and compared with all tiles by cosine similarity; the top 5 tiles are candidates.
3. ALIKED extracts up to 1,024 keypoints from the frame and each candidate tile (only the top-1 if its similarity is ≥ 0.82; at most 3 candidates; stop early at 25 inliers). LightGlue matches them and USAC_MAGSAC fits a homography with a 5 px threshold.
4. With ≥ 6 inliers the frame centre is projected through the homography into the tile and then into the map (lat, lon): a **verified fix**. Otherwise the centre of the top-1 tile is returned: a **fallback** (our adapter marks it `accepted = false` and scores it separately).
5. There is no motion model, no use of heading or height, and each frame is independent.

## Results
| Test | Run id | Result |
|---|---|---|
| S3-T1 as published, no hints (153 frames) | S3-T1-20261023-s0 | verified fixes on 52 frames (34.0 %), median error 0.07 m, p95 452 m, **precision@100 m 86.5 %** (7 of 52 verified fixes are wrong, up to 990 m); recall@10/50/100 m 29.4 %; fallback on 101 frames with median error 730 m (recall@100 m 11.8 %) |
| S3-T2 verified vs fallback separately | same run | verified: 0.07 m median; fallback: 730 m median, so the fallback is a coarse guess and is never a usable fix |
| S3-T1b OUR EXTENSION: frames de-rotated north-up with the compass yaw (not the author's method) | S3-T1b-20261023-s0 | 86 verified (56.2 %), recall@50 m 49.0 %, precision@100 m 87.2 %, p95 776 m |
| S3-T3 ablation: no scale normalisation (raw frame scale, map not resampled; 144 tiles) | S3-T3-noscale-20261023-s0 | 60 verified (39 %), precision@100 m falls to 60.0 %, recall@50 m 23.5 % (from 29.4 %): the 1024 px tile size only works when map pixels match the camera ground resolution |
| S3-T4 retrieval depth (1 / 3 candidates tried of top-5 / 10 tried of top-10) | S3-T4-top1, S3-T1, S3-T4-top10 | verified 49 / 52 / 61; recall@50 m 27.5 / 29.4 / 30.7 %; precision@100 m 85.7 / 86.5 / 77.0 %; p50 step 9.9 / 14.1 / 9.1 s (loaded CPU, not comparable): more candidates add few extra fixes and more wrong ones |
| S3-T5 terrain (substitute semantic map classes under the true position) | report section 3.4 | recall@50 m: building 18.9 %, vegetation 12.5 %, water 50.0 %, mixed 38.0 % (the 26 "water" frames are mostly the river bank and bridge region where strong edges exist; this is a class from our substitute labels, not real land cover). Fixes drop in blur/haze windows (recall@100 m 22.2 % vs 34.4 % clear) |
| S3-T6 real-time and constrained replay | serial runs, report section 3.5 | step time is 6-14 s on this CPU: the solution cannot keep up with a 5 Hz stream (see table) |
| S3-T7 fallback dataset replay and UAV-VisLoc subset 06 reproduction | – | **not run**: UAV-VisLoc is a 17.7 GB archive (Google Drive / one Hugging Face zip), too large for the disk allowance; MAFS unreachable |
| S3-T8 OUR EXTENSION: constant-velocity Kalman filter over the fixes (not the author's method) | S3-T1-20261023-s0-KF | mean step between consecutive outputs falls from 124 m to 13 m, but between-fix extrapolation drifts: median error 235 m, recall@50 m 37.9 % (raw verified fixes: 29.4 %). Smooths jumps, does not recover accuracy |
| S3-T9 SITL with a GNSS jam window | report section 4 | no scenario SIM-2..SIM-7 completes: the verified fixes arrive on 34 % of 1 Hz frames, the EKF raises its variance failsafe at about 68 s, the same as with no vision (SIM-1). With de-rotation (extension) fixes reach 56 % of frames and the EKF survives until about 110-117 s, but no mission completes |

**Plausibility check.** A 0.07 m median error on verified fixes looks too good. We checked the metric code on a synthetic trajectory (unit tests) and the frame alignment (the verified homographies have a median of 400 inliers and project the frame centre to within the rendering noise). The query is the map itself after a warp, blur/haze and noise; with 400 matching keypoints sub-pixel accuracy (0.18 m/px) is expected. It does not predict field accuracy.

## Failure analysis (T1; `results/s3/nidar/S3-T1-20261023-s0/failure_diag.json`; images with Esri crops are kept local and not committed)
Nine failed frames, chosen across the clear, blur and haze periods: t = 14, 23, 29 s (heading ≈ 0°: clear, blur, blur), 54, 57, 61 s (heading ≈ 85-90°: haze), 73, 76 s (heading 89°, clear), 95 s (heading 89°, blur).
- **The retrieval is mostly right.** The tile that contains the truth is rank 1-4 of 361 in 6 of the 9 frames (rank 1 at t = 61 s and 73 s even with the pipeline's own un-rotated input), rank 6 at t = 76 s, and rank 9-10 in the blurred frames at t = 29 and 95 s (rank 12-14 after de-rotation).
- **The verification fails.** Those frames give 0-5 inliers, below the 6 needed, so the pipeline falls back to the tile centre (about 700 m wrong on average here because tiles are 185 m wide and the first-ranked tile is not always the right one).
- **Cause 1: rotation.** From t ≈ 50 s the vehicle flies east (heading 90°) and then south-west; the frame is rotated by the heading but ALIKED/LightGlue are not rotation-invariant. At t = 73 s (clear air, rank-1 tile) there are only 5 inliers. De-rotating by the compass raises the fix rate from 34 % to 56 % (T1b).
- **Cause 2: blur and haze.** Fewer keypoints survive; in the blur window t = 23-29 s there are 5 inliers or fewer, and retrieval degrades (rank 9-14 at t = 29 s and 95 s).
- **Cause 3: a verified-but-wrong fix.** 7 of the 52 verified fixes are far off (up to 990 m) because the 6-inlier threshold is low and a wrong tile that shares repeated building texture can pass it; no gate (height/baro/IMU) exists to reject them (precision@100 m 86.5 %).

## Comparison with the author's claims
| Claim (UAV-VisLoc subset 06, real imagery, flights ≥ 405 m) | Ours (synthetic NIDAR, 200 m, same-map imagery) | Verdict |
|---|---|---|
| Top-1 tile recall 70 %, top-5 85 % | not measured as a separate metric; the true tile is in the top 4 in 6 of 9 failed frames | not compared |
| Median error 3.85 m, mean 14.20 m | verified fixes only: median 0.07 m; every frame counted (verified + tile-centre fallback): median 172.5 m, mean 380 m | **not reproduced** for an all-frames median; the verified-only median is better because the images share pixels with the map |
| Hit rate ≤ 50 m 88.5 %, ≤ 100 m 92.1 %, ≤ 500 m 96.8 % | all frames: 34.0 % / 41.2 % / 62.7 % (T1); with de-rotation 49.0 % / 49.0 % / 69.9 % | **not reproduced**; far lower on our rotating-heading video, which the notebook's north-up dataset does not contain |
The author's own caveat holds: forest, fields and water give few matches. Our video also rotates with the heading, which is not mentioned in the README.

## Verdict
- **Strengths:** shortest and simplest of the pipelines (one notebook); no training; the algorithm is easy to read; verified fixes are very accurate when they exist; every component is a pretrained public model.
- **Weaknesses:** no rotation handling (the author's data is north-up); a 6-inlier acceptance threshold with no height/attitude gate; a tile-centre fallback that is a 100-700 m error rather than a rejection; no tracking so every frame pays the full retrieval and matching cost (6-14 s per frame on CPU); notebook only, no licence.
- **Use when:** frames are north-up (or de-rotated with a compass) and the scale matches the map, an offline or high-latency fix source is acceptable, and a gating layer (EKF innovation test, baro, tracking) sits behind it. **Do not use** as the only position source on a moving platform, on an embedded CPU without a GPU, or where a licence is needed.
- **Risks observed:** tile size is in pixels, so the scale ratio is a hidden requirement (T3: precision drops to 60 % without it); results come from a map that equals the query source; no licence.
