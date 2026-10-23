# S2 analysis (uav-geolocalization: CAEVL + VIO + fuse.py)

Data label: **synthetic NIDAR-like** (map and frames from the same Esri imagery; optimistic). **VIO layer = OUR-VIO-SUBSTITUTE, not OpenVINS** (OpenVINS builds, was never run; see SETUP.md). **CAEVL encoder = our 2-epoch CPU-trained model (about 50 min), far weaker than the authors' GPU model.** The central result must be read with both caveats.

## How it works (from the code run)
Stages:
1. Map prep (offline): north-up tiles cut from the reference map at a fixed ground size (60/120/240 m, stride half a tile), resized to 256x256 gray.
2. Edge descriptor: OpenCV Canny (70/200) on the tile -> CNN encoder of CAEVL (`caevl.ae` encoder, 37.5 M params, 1024-d, PyTorch CPU) -> L2-normalised descriptor. Computed once per tile and stored as a float32 matrix (3.7 MB for 900 tiles).
3. Training (offline, reference imagery only): stage 1 autoencoder reconstructs Canny maps; stage 2 VICReg pulls together a clean tile and a randomly degraded copy (rotation, zoom, blur, vignette, lens mask) with variance/covariance terms to avoid collapse.
4. Query: camera frame -> (our adaptation) rectify to a north-up ground plane using attitude + baro altitude -> same Canny + encoder -> cosine nearest neighbour (matrix product; `faiss` not needed at this size) -> tile centre = absolute fix (corrected for the camera-axis offset).
5. VIO (relative motion): author uses OpenVINS (MSCKF, ROS 2). We used OUR-VIO-SUBSTITUTE: rectified ground images + `cv2.phaseCorrelate` between frames + compass yaw + baro height + gyro/accel tilt.
6. Fusion (`fuse.py` causal): keep a trailing 30 s buffer of fixes with the VIO position at each fix time; RANSAC on two-fix similarity transforms (inlier radius 60 m, at least 3 agree) keeps only mutually consistent fixes; all accepted fixes ever seen are fitted with a least-squares similarity (scale, yaw, offset) once they span 300 m; output = transform applied to the current VIO position. Modes `align` (offline windowed refit) and `ekf` (4-state filter, chi-square gate) are alternatives. The retrieval score is never used.
7. Optional corridor search: restrict candidate tiles to a disc around the motion prior.

## Results (all synthetic NIDAR-like; runs under results/s2/nidar/<id>/)
| Test | Result |
|---|---|
| S2-T1 VIO only (substitute, 764 frames at 5 Hz, path 1464 m) | final error 19.3 m, RMSE 61.1 m, median 61 m; end-error/path 13.2 m per km (1.3 %), linear-fit growth 21 m/km. Almost all error is a 40-60 m offset acquired in the first 10 s take-off transient (attitude filter lags the 0 to -30 deg pitch change), not growing drift. With truth attitude the same code gives RMSE 5.4 m (dev test, not kept). T1b degraded (3 deg yaw bias, -4 % scale): final 27.3 m, RMSE 49.7 m. Real OpenVINS drift unknown. |
| S2-T2 retrieval only, T=120 m, stride 60, full map 900 tiles, 764 frames | recall@50 m 1.6 %, recall@100 m 2.0 %, recall@250 m 9.6 %, median error 688 m, p95 1239 m; true tile median rank 86 of 900; true place within top-20 at 100 m: 36.6 %. After only 1 epoch of each stage: 1.0 % @100 m. Clear 2.9 % @100 m; blur / haze / blur+haze windows 0 %. Near chance (chance @100 m about 1-2 %). |
| S2-T3 fused, causal, full-map search (estimates.csv for SITL) | fused RMSE 790 m vs VIO-alone 61 m (whole flight); after warm-up (t >= 55.8 s) fused 990 m vs VIO 67 m. Final error 308 m vs 19 m. **Fusion is much worse than VIO alone.** Consensus accepted 101 fixes at arrival: precision 0 % (raw precision 2.0 %); ever accepted 405 with 0.7 % precision. Fitted scale 2.08 (wrong). |
| S2-T3c fused with corridor 200 m (T6 variant in the loop) | fused RMSE 439 m vs VIO 61 m; corridor centred on the fused prior drifts away from truth; 0 % accepted precision. |
| S2-T4 fuse.py batch modes on the same logs | causal (as shipped, with its non-causal pre-filter) RMSE 1057 m; align (offline) 434 m; ekf 790 m; all worse than VIO alone (61 m). Offline upper bound does not rescue it. |
| S2-T5 consensus off | RMSE 675 m, fitted yaw -34 deg, scale 2.66, vs 791 m with consensus on: rejection changes little because >98 % of fixes are wrong and wrong fixes agree with each other (similar-looking dense urban blocks). |
| S2-T6 corridor off vs on (T2 run, prior = VIO substitute + 200 m / 300 m radius) | off: recall@100 2.0 %, median 688 m, 900 candidates, search 0.12 ms. 200 m corridor: recall@100 23.7 %, median 136 m, 35 candidates, 0.025 ms. 300 m: see metrics.json corridor_300. Gain is mostly the geometric prior (any tile inside a 200 m disc is within about 136-230 m; median error equals the radius scale), not descriptor quality; recall@50 12 %. Search time is negligible either way (encode about 190 ms dominates, matches README remark). |
| S2-T7 tile size, 1 Hz (153 frames), stride T/2 | 60 m: 3721 tiles, recall@100 4.6 % (@50 2.6 %); 120 m: 900 tiles, 2.0 %; 240 m: 196 tiles, 2.0 % (stride 120 m caps accuracy near 85 m). Top-20 hit rate rises with tile size (30.7 / 39.9 / 51.0 %) simply because fewer tiles. All near chance; no tile size is meaningfully better. |
| S2-T3x sanity (synthetic fixes, NOT CAEVL) | With ground-truth fixes (sigma 30 m) mixed with random outliers, fusion RMSE vs VIO-alone 61 m: 10 % good -> 238 m (worse); 20 % good -> 31 m (-49 % error); 40 % good -> 25 m (-59 %). Without consensus: 368-452 m at every rate. Degraded VIO: 20 % good -> 37.5 vs 49.8 m; 40 % -> 25.3 vs 49.8 m. |
| S2-T8 real-time / constrained | implemented (`run_s2.py t8`), NOT run. |
| S2-T9 fallback dataset | not run; MUN-FRL link unknown/unreachable and no open mirror found (harness brief); attempt = none beyond the brief. |
| S2-T10 SITL | not run by S2. |

## Does fusion beat VIO alone? Is the 36 % reduction reproduced?
No on both counts, in our setting. Fusion increased RMSE by about 12x. Cause chain: under-trained encoder (2 % raw precision) -> the consensus test, designed for a ~18 % good-fix stream, finds spurious agreement -> wrong similarity transform. The sanity test shows the fusion layer itself does work (about 50-60 % lower RMSE) once 20-40 % of fixes are good, and fails at 10 %. Author claim: causal RMSE 617 -> 392 m (-36 %) on MUN-FRL with OpenVINS that has 34-40 % scale error and large drift; our VIO substitute starts at 61 m (1.3 % of path), so there is much less drift for fixes to remove: relative gain comparisons are not like for like. Claimed retrieval: R@1 @100 m 48-53 % on ViLD, 17.7 % on MUN-FRL; ours 2 %. Not reproduced; cannot attribute to the method vs our training budget (50 min CPU, 1800 tiles vs GPU, about 250 k tiles) - this is the main confound. Claimed 13.3 Hz on CPU (20 threads): not measured (lead's T8); single-frame encode on a loaded 2-thread box was about 190 ms.

## Failure analysis (T2/T3, 120 m model; figures in results/s2/figures/failures/ as query | expected | matched)
Retrieval fails everywhere; by condition (results/s2/figures/recall_by_condition.csv): clear 512 frames 2.9 % recall@100; haze 160 frames 0 %; blur 46 frames 0 %; blur+haze 46 frames 0 %. Time windows (error of top-1 fix, true-tile rank):
- t=10 s (clear, leg A-B start): 1219 m, rank 22; wrong tile of similar block structure; score 0.993.
- t=24 s (blur, sigma ~3): 762 m, rank 667; Canny edges wiped by blur.
- t=40 s (clear, mid A-B): 693 m, rank 224.
- t=58 s (haze 0.6): 923 m, rank 40; score 0.993 (confident and wrong).
- t=74 s (clear, B-C over river/bridge, low texture): 165 m, rank 28 (closest).
- t=98 s (blur + haze overlap): 931 m, rank 115.
- t=128 s (haze 0.55, C-D): 892 m, rank 423.
- t=145 s (clear, C-D): 741 m, rank 6 (nearly right).
Causes: undertrained encoder; scores are uninformative (wrong matches 0.95-0.99, same as README finding); dense repetitive Pune rooftops; blur removes edges; 30-deg camera tilt makes rectified corners blurry/black; stride 60 m caps localisation accuracy at about 42 m. Fusion failure windows: all after warm-up (t>55.8 s) with a wrong transform; error 700-1300 m.

## Verdict
Strengths: small and cheap (0.8-15 MB index, encoder-bound, search negligible), appearance-invariant idea, honest author docs, design that ignores retrieval score; the fusion layer demonstrably works when about 20 % or more of fixes are right; corridor prior is a cheap large gain. Weaknesses: needs a well-trained encoder per area and altitude (scale sensitive; no released weights, CPU training of useful quality is not feasible in an hour); fuse.py is a batch tool tuned to one flight (its "causal" CLI applies a non-causal pre-filter; VIO-alone baseline there is aligned with fixes from the whole flight; accepted fixes are re-appended every step); needs yaw, altitude and attitude (or good VIO attitude); tilted cameras need ground-plane rectification; breaks under blur/haze; consensus can latch onto consistent wrong matches. Use when: a trained encoder exists for the terrain/altitude, GPU-trained, fix hit rate >= 20 %, and VIO drift is large. Do not use as shipped on this data.

## Risks observed
Harness: `run_replay.run` sorts frame files as strings (10.000 before 2.000), which breaks any temporal adapter; harness hints give pitch 0 though truth pitch is -30 deg in cruise (brief says ~0); hint yaw is in degrees although schema/brief say radians. S2 uses its own driver and `yaw_unit='deg'`. Disk/memory pressure (training 4-8 GB RAM). Undertrained model result must not be read as the method's ceiling.

## Lead commands for T8 (run alone; each prints latency/RSS into results/s2/nidar/S2-T8-<mode>-20261023-s0/)
```
cd solutions/s2_uav_geolocalization && . env/bin/activate
OMP_NUM_THREADS=4 python run_s2.py t8 --mode realtime --threads 4
OMP_NUM_THREADS=1 taskset -c 0 python run_s2.py t8 --mode constrained --cores 0 --threads 1
```
Main SITL file: results/s2/nidar/S2-T3-20261023-s0/estimates.csv (fused, VIO-only until t=55.8 s; note it is worse than the VIO-only column in extras.csv; `S2-T1-20261023-s0/estimates.csv` is the VIO-substitute alone).
