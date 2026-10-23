# S5 SWA-PF: ANALYSIS (CPU, synthetic NIDAR-like data, substitute semantic map)

READ FIRST (honest framing)
- All NIDAR numbers are on SYNTHETIC NIDAR-like frames (nadir re-render, 1280x800 gray, 200 m AGL) rendered from the SAME Esri map the filter matches against.
- The semantic MAP is OUR SUBSTITUTE: the repo's drone SegFormer-B0 (`logs/8_b0`) run on windows of `data/nidar/map_esri_z19.png` (resampled to 0.18 m/px, gray, 1280x800 windows), merged to the repo's classes, majority-pooled to 1 m/px (T5: 2 m/px). Frame labels come from the SAME segmenter on frames rendered from the same map, so map/frame agreement is optimistic. This is NOT the paper's U-Net map labelling (the paper's satellite labels cost >60 min per map image by hand annotation; no U-Net weights are shipped).
- Odometry is NOT ground truth: velocity (north/east/up) = finite difference of truth.csv + Gaussian noise (sigma 0.5 m/s north/east, 0.3 m/s up) and heading = truth yaw + N(0, 1.5 deg), passed through `Adapter.set_odometry(v_north, v_east, v_up, yaw_deg)` before each `step()` (the harness hints carry no odometry). The velocity is a world-frame vector, i.e. it presumes a compass/velocity source. Particle noise (adapter): sigma 8 m in x and y, 8 m altitude, 0.2 deg yaw per step, mean 0 (repo: mean +2, sigma 15 px).
- Replay is 1 Hz (every 5th frame, 153 frames), 2 threads, nice, CPU shared with 3-4 other jobs (load 8-11): latencies are inflated and NOT valid as an embedded timing figure. 5 Hz (764 frames) was not run: 5-10 s per frame x 764 = >1.5 h per run.
- Particle counts: 20,000 for the main run (the paper's number), 1,000 to 40,000 in the sweep; the repo's resampler keeps 80 % of the particles per frame down to a floor of 1000, so counts fall to 1000 after about 14 frames.

## 1. How it works (written from the code that was run)

Plain language. The filter keeps a cloud of guesses ("particles"), each a guess of the drone's map position (x, y), altitude and heading. Every frame:
1. The drone image is turned into a coarse "what is this" picture by a small neural net (SegFormer-B0, 7 classes, repo weights `logs/8_b0`). Only four of its classes matter downstream: ground/other, building (roof and wall are merged: `pr==5 -> 1` in SWAPF.py), road, vegetation. Vehicle (4) and water (6) are produced but ignored by the weights.
2. The map is a satellite image that was labelled once, offline, with the same 4 classes (`label.png`). For every class a distance map is precomputed: for each map pixel, the distance (clipped at 100 px) to the nearest pixel of that class.
3. Each particle cuts out the piece of that class-distance map the drone would see from its guessed altitude (`cut = alt * cam_view * height_to_pix / 2` pixels each side), rescales it to 400x400 (so altitude changes only the cut size, not the comparison size), and compares it with the drone class image, which was pre-rotated in 100 steps of 3.6 deg (25 rotations x 4 quarter turns) and rescaled to 400x400. The score is the sum over pixels of "drone says class c here" x "distance to nearest class c in the map crop", multiplied by a weight that grows with distance from the crop centre (`wan`), plus a huge penalty if the crop sticks out of the map. Low sum = good match. Particle weight = 400*400 / (sum^2).
4. Particles are resampled by weight (80 % of them survive each frame, floor 1000), then moved with the odometry (distance and bearing of motion, heading change) plus Gaussian noise in x, y, altitude and yaw.
5. Position estimate: when the cloud's spread is below a tenth of its initial spread, the particles are clustered (DBSCAN) and the cluster centre is taken (`center()` in PFtool.py; it also prints the error against ground truth, which is only a diagnostic).

Initialisation (PFtool, hard-coded `init_method_mode`): 0 full random over map x, y, altitude and yaw; 1 layered (4 altitude floors, same xy/yaw); 2 xy sampled on building pixels ("semantic alignment").

### What the "odometry" in SWAPF.py really is (leakage)
`SWAPF.py` reads `gps.txt` (ground-truth latitude, longitude, height, heading of every frame) and calls `particles.move(lat - old_lat, lon - old_lon, height - old_height, theta, old_theta)`. So the motion model receives the exact, noise-free ground-truth displacement, altitude change and heading of the drone, and the particle noise (gauss sigma 15 px in x,y, 8 m in z, 0.2 deg in yaw) is the only uncertainty. Initial particles are random (no pose leak), but the heading is known exactly after the first frame (all particles inherit the true heading change, the true heading offset is only found by the 100-way yaw search). `init_particles_goal`/`update_goal_1` put the ground truth in a "goal" particle used only for drawing. Hence the paper's 6.57 m is a filter with perfect relative motion and heading; there is no drift to fight. `center()` is commented out in the demo, so the demo as shipped saves overlay pictures only and outputs no estimate. Our replay replaces this with noisy telemetry odometry (see below).

## 2. Architecture (data-flow stages)
1. Frame (BGR 1280x800) -> optional extension: ortho-rectification with IMU roll/pitch (used only for the oblique stress set).
2. SegFormer-B0 (PyTorch 1.10 CPU, 512x512 letterbox, 7 classes) -> per-pixel class image; roof merged into building.
3. 100 pre-rotated 400x400 4-channel binary class images (torchvision rotate x25 + rot90 x3), multiplied by the bowl weight; integral images (adapter speed-up; repo does the same sum per particle).
4. Offline: satellite map -> label.png (paper: U-Net-VGG, 4 classes; ours: same SegFormer) -> per-class clipped distance transforms (cv2.distanceTransform) + padded copies.
5. Particle array (N x 6: x, y, altitude, yaw, weight, cumulative weight); weight = 160000/sum^2; resampling by searchsorted (80 % kept, min 1000).
6. Motion update with odometry (distance, bearing, yaw change) + Gaussian noise.
7. Spread check + DBSCAN (scikit-learn) -> cluster centre (x, y, altitude, circular-mean yaw) -> lat/lon through map georeference; accepted only when converged and the main cluster holds >= 50 % of particles.
## 3. Results (synthetic NIDAR-like, nadir frames, 1 Hz, seed 0 unless stated; metrics from harness/metrics.py `summarize`)

Frames: 153 for T2; 40 (t = 0..39 s) for T3/T4; 60 for T5. "converged and stays" = first accepted time after which every accepted fix is within 50 m (my own addition, from run_s5.py). Recall is over ALL frames, including the unavoidable convergence period.

| Test | Status | Result |
|---|---|---|
| S5-T1 reproduction on MAFS 200 m | NOT RUN | MAFS only on Baidu Pan (unreachable); no open mirror (3 attempts, results/s5/mafs/MAFS_unavailable.md) |
| S5-T2 NIDAR replay, 20,000 particles, init random | DONE | fix rate 93.5 % (143/153), median 2.46 m, p95 6.42 m, RMSE 3.53 m, max 11.3 m, recall@10 92.2 %, recall@50/100 93.5 %, first fix at t=10 s; all 10 unaccepted frames are t=0..9 s (convergence). run: results/s5/nidar/S5-T2-20261023-s0 |
| S5-T3 particle sweep (40 frames) | DONE | see table below |
| S5-T4 initialisation (n=5000, 3 seeds, 40 frames) | DONE | see table below |
| S5-T5 map resolution 1 m/px -> 2 m/px (n=5000, 60 frames) | DONE | median 3.44 -> 10.69 m, p95 9.7 -> 18.2 m, RMSE 4.86 -> 11.57 m, max 11.2 -> 18.4 m; converged at 37 s vs 32 s (fix rate 38 % vs 47 % of 60 frames). Peak RSS 2314 -> 1679 MB |
| S5-T6 feature-poor scenes | DONE (post-hoc on T2) | see table below |
| S5-T7 constrained replay | IMPLEMENTED, NOT RUN | `bash solutions/s5_swa_pf/constrained_replay.sh 2000` (2 cores, 6 GB address cap, 5 Hz real-time, drops frames while busy). Expect almost all 5 Hz frames dropped: the filter needs 5-10 s per frame even at 1000 particles on this CPU |
| S5-T8 cross-area | NOT RUN | needs MAFS (two areas) |
| S5-T9 SITL estimates | DONE at 1 Hz | results/s5/nidar/S5-T2-20261023-s0/estimates.csv (t,lat,lon,alt,yaw,conf,accepted at 0,1,2,...,152 s); 5 Hz not possible on this CPU. Lead: hold/interpolate. accepted=false for t<10 s |

### T3 particle sweep (initial particle count; seed 0; 40 frames; times under CPU load 8-11, 2 threads)
| initial N | accepted fixes | median err (accepted) | converged-and-stays<=50 m | first step | median step | peak RSS |
|---|---|---|---|---|---|---|
| 1,000 | 18 % | 817.6 m (false lock, wrong) | never | 11.7 s | 5.3 s | 2255 MB |
| 5,000 | 8 % | 0.91 m | 37 s | 9.3 s | 4.5 s | 2247 MB |
| 20,000 | 75 % | 1.85 m | 10 s | 37.3 s | 9.5 s | 2337 MB |
| 40,000 | 75 % | 1.98 m | 10 s | 66.7 s | 8.3 s | 2293 MB |
Time per step is dominated early by the number of particles (about 1.5 ms per particle with my integral-image speed-up; the repo's own loop is about 17 ms per particle here) and later by a constant of about 4-5 s (SegFormer 1-3 s + 100 rotated label images + integral images). Peak RSS (about 2.3 GB) is mostly torch + SegFormer + distance maps; it barely depends on N.

### T4 initialisation (n=5000, seeds 0,1,2, 40 frames; failure = no stable convergence (<=50 m) within 40 s)
| init | converge-and-stay time per seed | failures |
|---|---|---|
| full random | 37 s, none, 22 s | 1/3 never converged correctly within 40 s (seed 1: still 600 m off), |
| layered random (4 altitude floors) | none, none, none | 3/3 |
| semantic alignment (xy on building pixels) | none (616 m off), 10 s, none (3 wrong accepted fixes, 581 m) | 2/3, with false accepted fixes |
Caveat: only 3 seeds x 40 frames x 5,000 particles; at 20,000 particles full random converged in 10 s (T2, T3). With 5,000 initial particles the correct mode is often lost in the first resamples (80 % kept per frame, noisy odometry, coarse labels). Layered init gives 4 copies of each xy so it has 4x fewer distinct positions. False confident fixes exist (cloud collapses on a wrong place: spread below threshold but 500-800 m off): the spread/DBSCAN test is not a correctness test.

### T6 scene classes (post-hoc on T2, converged frames only; class = share of OUR substitute map labels in the 100 m square under the true position; 'water' = segmenter label 6 >10 %, may be shadows/dark roofs, not verified water)
| scene | frames | median raw error | p90 |
|---|---|---|---|
| building >70 % | 53 | 2.82 m | 5.74 m |
| vegetation >40 % | 24 | 3.16 m | 8.41 m |
| water-labelled >10 % | 26 | 3.49 m | 5.30 m |
| mixed | 50 | 1.59 m | 3.08 m |
| blur windows | 18 | 2.01 m | 3.03 m |
| haze windows | 41 | 3.39 m | 6.12 m |
| clear | 103 | 2.01 m | 5.10 m |
No feature-poor breakdown was observed after convergence: because the particles move with (noisy) odometry and the map labels came from the same segmenter, a 4-class match survives low-texture windows; haze and vegetation are the worst (about +1.4 m median). The true breakdown risk is in the acquisition phase (below) not after lock.

## 4. Oblique-frame stress results (first render, camera body-fixed, pitch -30 deg in cruise): results/s5/nidar_oblique
- S5-T2 as-is (20,000 particles): fix rate 65 %, median 106.8 m, p95 119.9 m, RMSE 103.3 m, recall@10 and @50 = 0 %. The filter locks, but on the image-centre ground point, which sits about 115 m behind the drone (200 m x tan 30 deg), so the error equals the geometric offset. Repo/paper estimate x, y, altitude, yaw only: roll/pitch are not modelled, so a tilted camera breaks it.
- S5-T2b with my ortho-rectification extension (IMU roll/pitch + baro with noise 0.5 deg / 1 m): the filter locked correctly at t=37 s (error 2.6 m) but lost lock after the first turn (about t=42 s; error 100-220 m for the rest; median 172 m): rectified oblique frames far from nadir are low resolution and distorted, and the turn changes tilt. Fix rate 55 %. Both runs are evidence for the feature-poor/robustness story, not the headline.
- register_check.txt / align_check_res1.0.json in that folder show the 107 m offset (template matching) that led to the data defect report to the lead.

## 5. Failure analysis (nadir T2 plus sweeps; figures: results/s5/figures/failures/*.png = frame | map crop at TRUE position | map crop at ESTIMATED position)
1. t=0-9 s (T2): no fix, cloud still spread 300 m down to 30 m; random initialisation needs about 10 s (resampling shrinks 20,000 -> 1000). Cause: acquisition phase of a global filter; figure t=4.
2. t=74 s (veg, 11.3 m worst converged error): vegetation-dominated window; probable cause (not verified): tree/ground labels differ between map and frame labelling; figure t=74.
3. t=135 s (building, 7.6 m) and t=116 s (mixed, 5.1 m): probable cause (not verified): heading noise (1.5 deg) plus the 3.6 deg rotation quantisation; errors are only 5-8 m.
4. t=60 s and t=50 s (haze 48.9-68.7 s, 7.1 m and 4.8 m): low contrast, SegFormer labels shift; error rises from 2 to 3.4 m median but lock holds.
5. n=1000 initial particles (T3), init modes 1 and 2 (T4): false lock at 540-820 m; wrong cluster accepted (581 m, 3 fixes in init2/seed2; 817 m median in n=1000). Not weather related; cause: too few particles/modes lost during the first resamples, and acceptance test (spread < 10 % of initial) cannot detect a wrong mode.
6. Oblique set (section 4): 30 deg camera tilt, 115 m systematic offset; after rectification loss of lock at the turn (t about 42-50 s).
7. Blur windows (21.4-30.6 s and 94.7-103.9 s) caused no failure here (median 2.0 m): SegFormer labels are coarse anyway.

## 6. Comparison with the author's claims
| Claim | Paper (MAFS, real, GPU) | Ours (synthetic NIDAR-like, CPU, substitute map) | Verdict |
|---|---|---|---|
| RMSE | 6.57 m | 3.53 m (T2, accepted fixes) | not comparable: synthetic, map and frames from the same imagery and segmenter; at 1 m/px labels and noisy (not GT) odometry; NOT reproduced on MAFS |
| recall@10 m | 97.4 % | 92.2 % over all frames (100 % of accepted frames <=11.3 m) | partly consistent in magnitude; same caveats |
| 20,000 particles in ~4.9 s on GPU | yes | 37 s first step (20,000), steady 5-10 s per frame at 1000 particles on a loaded 2-thread CPU; the repo's own per-particle loop is about 17 ms per particle (about 6 min per frame) | CPU is 8-75x slower; with my integral-image rewrite 1.5 ms per particle |
| works at several altitudes (4-DoF) | yes | altitude is a state (150-250 m search); not stressed (flight at 200 m) | not tested |
| memory efficient | no figure | peak RSS 2.3 GB at 1 m/px over 0.9 km x 0.9 km (1.7 GB at 2 m/px); mostly torch + SegFormer; per-class float32 distance maps (10 copies incl. padded) | note: scales with map pixels (full 1.9 km map at 0.5 m/px would need several GB) |
| roll/pitch not estimated | stated | breaks on a 30 deg tilted camera (115 m offset) | confirmed limitation |
| MAFS reproduction | 3 trajectories Hangzhou | not run | dataset unavailable |

## 7. Verdict
Strengths: genuinely cheap map representation (4-class labels instead of image tiles or descriptors; a 1 m/px label map of 0.9 km square is under 1 MB), no map feature database, works with any segmenter that gives the 4 classes, tolerant to blur/haze after lock, fast convergence (10 s) with 20,000 particles, centimetre-to-metre-level when labels agree. Weaknesses: needs a relative-motion source with world-frame velocity and heading (the demo even uses ground truth GPS differences); needs consistent semantic labels on both sides (the paper's labelling is manual/U-Net; here the same segmenter on both which is optimistic); global start is slow and unreliable on CPU (n=1000-5000 often locks onto wrong places, acceptance test cannot tell); no roll/pitch handling; per-frame cost on CPU is seconds; shipped code is a demo (hard-coded Windows path and ground-truth motion, `center()` commented out); no licence declared; dataset unobtainable here.
Use when: a good odometry source exists (VIO or velocity+compass), a labelled map is available, and a GPU (or a rewritten vectorised filter) is used, as a coarse global relocaliser / drift reset. Do not use as the sole GNSS-denied localiser on a CPU companion computer, on tilted cameras, or in scenes where the segmenter cannot separate classes.
Known risks observed: false convergence; strong dependency on particle count (5,000 not enough here); disk/RAM scale with map pixels (map arrays are duplicated 10x as float32 distance maps); upstream Chinese comments, dead code and hard-coded modes; reproducibility 0 of 3 on data; no licence.
(failure PNGs contain Esri map crops: kept local only, not pushed)
