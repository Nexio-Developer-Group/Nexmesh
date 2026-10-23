# S2 analysis (uav-geolocalization: CAEVL + VIO + fuse.py)

Data label: **synthetic NIDAR-like** (map and frames from the same Esri imagery; optimistic). **VIO layer = OUR-VIO-SUBSTITUTE, not OpenVINS** (OpenVINS builds, was never run; see SETUP.md). **Primary data = gimbal-stabilised NADIR render (data/nidar). The earlier body-fixed 30-degree-oblique render is kept as a stress result (appendix, results/s2/nidar_oblique/).** **CAEVL encoder = our 2-epoch CPU-trained model (about 50 min), far weaker than the authors' GPU model.** The central result must be read with both caveats.

## How it works (from the code run)
Stages:
1. Map prep (offline): north-up tiles cut from the reference map at a fixed ground size (60/120/240 m, stride half a tile), resized to 256x256 gray.
2. Edge descriptor: OpenCV Canny (70/200) on the tile -> CNN encoder of CAEVL (`caevl.ae` encoder, 37.5 M params, 1024-d, PyTorch CPU) -> L2-normalised descriptor. Computed once per tile and stored as a float32 matrix (3.7 MB for 900 tiles).
3. Training (offline, reference imagery only): stage 1 autoencoder reconstructs Canny maps; stage 2 VICReg pulls together a clean tile and a randomly degraded copy (rotation, zoom, blur, vignette, lens mask) with variance/covariance terms to avoid collapse.
4. Query: camera frame -> (our adaptation) rotate to north-up by compass yaw and scale by baro altitude (the nadir data needs no tilt correction; for the oblique data a full ground-plane rectification through IMU-estimated roll/pitch was used) -> same Canny + encoder -> cosine nearest neighbour (matrix product; `faiss` not needed at this size) -> tile centre = absolute fix (corrected for the camera-axis offset).
5. VIO (relative motion): author uses OpenVINS (MSCKF, ROS 2). We used OUR-VIO-SUBSTITUTE: north-up ground images + `cv2.phaseCorrelate` between consecutive frames + compass yaw + baro height.
6. Fusion (`fuse.py` causal): keep a trailing 30 s buffer of fixes with the VIO position at each fix time; RANSAC on two-fix similarity transforms (inlier radius 60 m, at least 3 agree) keeps only mutually consistent fixes; all accepted fixes ever seen are fitted with a least-squares similarity (scale, yaw, offset) once they span 300 m; output = transform applied to the current VIO position. Modes `align` (offline windowed refit) and `ekf` (4-state filter, chi-square gate) are alternatives. The retrieval score is never used.
7. Optional corridor search: restrict candidate tiles to a disc around the motion prior.

## Results (primary: nadir data; runs under results/s2/nidar/<id>/)
| Test | Result |
|---|---|
| S2-T1 VIO only (substitute, 764 frames at 5 Hz, path 1464 m) | final error 7.9 m, RMSE 5.0 m, 5.4 m per km of path (0.54 %). T1b degraded (3 deg yaw bias, -4 % scale): final 29.0 m, RMSE 36.5 m, 19.8 m/km. Uses noise-free truth yaw and baro altitude, so this is optimistic; real OpenVINS drift unknown. |
| S2-T2 retrieval only (120 m tiles, 900 tiles, 764 frames, full map) | recall@50 m 3.8 %, @100 m 4.6 %, @250 m 9.2 %; median error 656 m, p95 1146 m; true tile median rank 64 of 900; true place in top-20 within 100 m: 43.5 %. Clear weather 6.8 % @100 m; haze 0 %, blur 0 %, blur+haze 0 %. Near chance. |
| S2-T3 fused causal, full-map search (estimates.csv for SITL) | fused RMSE 798 m vs VIO alone 5.0 m (whole flight); after warm-up (t >= 25.4 s) 874 m vs 5.5 m. Final 316 m vs 7.9 m. Accepted at arrival 84 fixes, 7.1 % precise (raw precision 4.6 %); fitted scale 2.95, yaw -30 deg (wrong). Fusion is far worse than VIO alone. |
| S2-T3c fused with a 200 m corridor around the motion prior | fused RMSE 116 m (whole flight; 129 m after warm-up at 29 s) vs 5.0 m VIO. Raw fixes in the corridor are 31.8 % within 100 m and consensus accepts 301 fixes with 36.9 % precision, but 69 % wrong is still enough to corrupt the fitted transform. |
| S2-T4 fuse.py batch modes on the T3 logs | causal as shipped (with its non-causal pre-filter) RMSE 804 m; align (offline) 576 m; ekf 837 m; VIO alone 5 m. The offline bound does not rescue it. |
| S2-T5 consensus off | RMSE 580 m (fit yaw -17 deg, scale 2.88) vs 799 m with consensus on; consensus helps slightly but >95 % of fixes are wrong and wrong fixes agree with each other. Streaming port reproduces the adapter to 2e-8 m. |
| S2-T6 corridor off vs on (T2 run, prior = VIO substitute) | off: recall@100 4.6 %, median 656 m, 900 candidates, 0.08 ms. 200 m radius: recall@100 35.6 % (@50 24.1 %), median 121 m, 35 candidates, 0.03 ms. 300 m radius: 18.6 % (@50 13.6 %), median 198 m. The gain is mostly the geometric prior (median error is about the corridor scale), not better descriptors; encode (about 190 ms on the loaded 2-thread box) dominates, search time is negligible. |
| S2-T7 tile size at 1 Hz (153 frames), stride T/2 | 60 m: 3721 tiles, recall@50 2.6 %, @100 4.6 %; 120 m: 900 tiles, 3.9 % / 4.6 %; 240 m: 196 tiles, 0 % / 0 % (stride 120 m caps accuracy near 85 m and one 240 m query is mostly black corners). All near chance, no tile size is meaningfully better. |
| S2-T3x sanity (synthetic fixes with 30 m noise, NOT CAEVL output) | The fusion layer cannot help a 5 m VIO: with the substitute VIO, 40 % good fixes still gives RMSE 5 vs 5 m (-9 %) and 10-20 % good fixes make it worse (168 m, 20 m). With a degraded VIO (RMSE 37 m): 20 % good -> 12 m (67 % lower), 40 % good -> 18 m (50 % lower), 10 % good -> 125 m (worse). Consensus off: 344-608 m at every rate. |
| S2-T8 real-time / constrained | implemented (`run_s2.py t8`), NOT run (lead runs). |
| S2-T9 fallback dataset | not run: MUN-FRL unreachable, no open mirror known. |
| S2-T10 SITL | not run by S2. |

## Does fusion beat VIO alone? Is the 36 % reduction reproduced?
No, and not reproduced. On nadir data our VIO substitute is already very good (RMSE 5 m, 0.5 % of path), so there is little drift for fixes to remove, and our under-trained encoder (4.6 % of raw fixes right) feeds the fusion mostly wrong fixes, so fused RMSE is about 160x worse (798 m vs 5 m). Author claim: causal RMSE 617 -> 392 m (-36 %) with a real OpenVINS that has a 34-40 % scale error, on MUN-FRL where 18 % of fixes are right. Our VIO layer is not comparable with that, so relative gain is judged only as fused vs VIO-alone here: negative. The sanity test shows the consensus + similarity-fit layer helps (50-67 % lower RMSE) only when the VIO is poor (tens of metres) and 20-40 % of fixes are good; it fails at 10 %. Claimed retrieval R@1 @100 m 48-53 % (ViLD), 17.7 % (MUN-FRL): ours 4.6 %. We cannot separate method quality from our training budget (50 min CPU, 1800 tiles, versus GPU, about 250 k tiles): that is the main confound. 13.3 Hz on CPU: not measured (lead T8); single encode about 190 ms on our loaded 2 threads.

## Failure analysis (T2/T3, 120 m model; crops query | expected | matched in results/s2/figures/failures/)
By condition (recall_by_condition.csv): clear 512 frames 6.8 % recall@100; haze 160 frames 0 %; blur 46 frames 0 %; blur+haze 46 frames 0 %. Top-1 error and true-tile rank:
- t=10 s (clear, take-off leg): 1090 m, rank 2, score 0.995 (confident, wrong; nearly right ranking).
- t=24 s (blur): 920 m, rank 313; edges wiped by blur.
- t=40 s (clear): 302 m, rank 218.
- t=58 s (haze 0.6): 657 m, rank 21.
- t=74 s (clear, over river/bridge, low texture): 650 m, rank 100.
- t=98 s (blur + haze): 892 m, rank 240.
- t=128 s (haze 0.55): 745 m, rank 320.
- t=145 s (clear, last leg): 957 m, rank 46.
Causes: under-trained encoder; scores uninformative (wrong matches score 0.98-0.995, as the README says); repetitive dense rooftops; blur/haze suppress Canny edges; stride 60 m caps accuracy at about 42 m. Fusion failures: everything after warm-up (t > 25 s) follows a wrong transform; error 300-1350 m.

## Verdict
Strengths: tiny index (0.8-15 MB), encoder-bound runtime with negligible search, appearance-invariant concept, honest docs, design that ignores the retrieval score; a corridor prior is a cheap large gain (4.6 -> 35.6 % recall@100). Weaknesses: needs a well-trained encoder per area and altitude (no released weights; a CPU hour is not enough); fuse.py is a batch tool tuned to one flight (its "causal" CLI applies a non-causal pre-filter; VIO-alone baseline is aligned with fixes from the whole flight; accepted fixes are re-appended every step); needs yaw, altitude (and attitude); degrades under blur/haze; consensus latches onto consistent wrong matches and cannot improve a good VIO. Use when a GPU-trained encoder exists for the terrain and altitude, fix hit rate is at least 20 %, and VIO drift is large. Do not use as shipped on this data.

## Risks observed
Harness: `run_replay.run` sorted frames as strings (breaks temporal adapters; S2 uses its own driver); hint yaw is in degrees (adapter `yaw_unit='deg'`). Disk/memory pressure (stage-2 training 4-8 GB RAM; docker export ENOSPC). Under-trained model must not be read as the method ceiling.

## Appendix: oblique stress result (results/s2/nidar_oblique/, "body-fixed 30-deg-oblique camera")
Before the dataset fix the camera was body-fixed with 30 deg nose-down pitch. S2 handled it by IMU tilt estimation (complementary filter, median error 0.1-0.2 deg, transients to 13 deg) plus ground-plane rectification. Results then: VIO substitute RMSE 61 m (final 19 m; the error was a 40-60 m offset from the take-off tilt transient); retrieval recall@100 2.0 %, median 688 m; corridor 200 m 23.7 %; fused RMSE 790 m (fuse.py batch causal 1057, align 434, ekf 790) vs 61 m VIO; consensus off 675 m; tile size 60/120/240: 4.6/2.0/2.0 %; sanity with good fixes at 20-40 % gave 49-59 % lower RMSE than that 61 m VIO. Conclusion identical: the weak encoder breaks fusion; a poorer VIO (as in the oblique case) is where fusion has room to help. Old figures: results/s2/figures_oblique/.

## Lead commands for T8 (run alone; outputs in results/s2/nidar/S2-T8-<mode>-20261023-s0/)
```
cd solutions/s2_uav_geolocalization && . env/bin/activate
OMP_NUM_THREADS=4 python run_s2.py t8 --mode realtime --threads 4
OMP_NUM_THREADS=1 taskset -c 0 python run_s2.py t8 --mode constrained --cores 0 --threads 1
```
Main SITL file: results/s2/nidar/S2-T3-20261023-s0/estimates.csv (VIO-only until t=25.4 s, then fused; fused is worse than the VIO-only track in S2-T1-20261023-s0/estimates.csv, which is the better input for SITL).
