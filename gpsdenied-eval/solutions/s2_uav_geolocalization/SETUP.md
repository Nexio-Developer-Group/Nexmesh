# S2 setup log (uav-geolocalization = CAEVL + OpenVINS + fuse.py)

All results: **synthetic NIDAR-like** data (map and frames both from the same Esri imagery, so the appearance gap is optimistic).
**The VIO layer in every result is `OUR-VIO-SUBSTITUTE`, NOT OpenVINS** (see "OpenVINS" below).

## Components, commits, licences
| Component | Source | Commit | Licence |
|---|---|---|---|
| uav-geolocalization (fusion tools, report) | NijatAbbasov06/uav-geolocalization | 9983dca3e9e14df22d33173cfab6f5b3ca7e88a8 | none shown on repo page / no LICENSE file |
| CAEVL fork | NijatAbbasov06/caevl | 43a28b9ec0f31d0bf09e69bfb01802bf6cd19140 | no LICENSE file in fork (README says forks keep upstream licence; upstream Tristan-Amadei/caevl @266ba3a also has no LICENSE file) -> treat as all rights reserved, internal evaluation only |
| OpenVINS fork (branch alto_integration) | NijatAbbasov06/open_vins | 71eedce9614904fbc9e972fa030b5c7fb06ee413 | **GPLv3** (LICENSE file). Isolated in `openvins_gpl/` (Dockerfile, ROS bag script, config), never imported by other code |
| torch 2.14.1+cpu, torchvision, timm, faiss-cpu, opencv-headless, numpy ... | PyPI / download.pytorch.org/whl/cpu | see lock.txt | BSD / Apache / MIT |
| Weights | trained by us on the NIDAR reference map (no released checkpoint exists in either repo) | `weights/stagetwo.pth` 362 MB (not committed) | n/a |
| Esri World_Imagery map | benchmark only | | not redistributable |

CPU: Intel Xeon @ 2.10 GHz, 4 cores shared with other agents (torch/OpenCV limited to 2 threads, `nice`). No GPU: a GPU run is impossible here.

## Commands that worked
```
git clone <caevl fork> caevl_src ; git clone -b alto_integration <open_vins fork> openvins_gpl/src   # both clone fine via the git proxy (curl/gh API 403, git works)
python3 -m venv env && . env/bin/activate
pip install --index-url https://download.pytorch.org/whl/cpu torch torchvision
pip install numpy opencv-python-headless pyyaml tifffile tqdm timm einops faiss-cpu loguru scikit-learn Pillow scipy pandas matplotlib gdown   # gdown is imported but missing from caevl requirements.txt
python make_train_tiles.py 1800                      # 1800 train + 240 val reference tiles (60/120/240 m, random positions, 256 px)
cd caevl_src
python caevl/ae/train.py -c ../configs/ae_nidar.yml            # stage 1 (1 epoch)
python caevl/ft_stage/train.py -c ../configs/stagetwo_nidar.yml  # stage 2 (epoch 1), then again with --load-weights (epoch 2)
cd .. ; python run_s2.py t1 ; python retrieval_eval.py --tag S2-T2 --tile 120 --corridors 200,300 --vio <T1 vio_track.csv>
python run_s2.py t3 ; python run_s2.py t3 --tag S2-T3c --corridor 200 ; python post.py t4|t5|t9 ; python make_figs.py
retrieval_eval.py --tag S2-T7-tile{60,120,240} --tile {60,120,240} --step 5
```
Wall clock from clone to first working output (a retrieval number): about 95 min, dominated by CPU training and a loaded machine.

## CAEVL training (CPU, budget < 60 min, spent about 50 min)
Reference-only training as in the README: stage 1 autoencoder on Canny edge maps (172.5 M params, encoder 37.5 M, latent 1024), stage 2 VICReg on clean/degraded tile pairs. Data: 1800 tiles from the NIDAR map (mixed 60/120/240 m footprints so one model serves the T7 sweep), val 240. Seed 0.
| step | epochs | batch | wall | train loss | val loss |
|---|---|---|---|---|---|
| stage 1 (L2, no DINOv2 perceptual loss) | 1 | 32 | 16.5 min | 0.220 | 0.170 |
| stage 2 VICReg | 1 | 32 | 16.0 min | 24.5 | 19.0 |
| stage 2 VICReg (resumed) | +1 | 32 | 16.0 min | 21.3 | 17.97 |
Total about 49 min. The authors trained on GPU for up to 20 epochs on ~250k tiles; we used 2 epochs-equivalent on 1800 tiles, so this encoder is severely under-trained (see ANALYSIS.md: retrieval is near chance). Logs: results/s2/training/. Stage-1 checkpoint was deleted for disk (resume of stage 2 needed `load_weights_backbone: False`).

## Tile database
Own script (`s2core.Retriever`; the repo's `munfrl_map_tiles.py` is MUN-FRL-filename specific and `caevl` tile tools target IGN data). North-up tiles, stride = tile/2, 256 px, descriptors L2-normalised, computed once.
| tile | stride | tiles | index | build time (2 threads, loaded box) |
|---|---|---|---|---|
| 60 m | 30 m | 3721 | 15.2 MB | 285 s |
| 120 m (default) | 60 m | 900 | 3.7 MB | 68-85 s |
| 240 m | 120 m | 196 | 0.8 MB | 16 s |

## Adaptations needed (not in the author's code)
1. **Camera tilt.** The synthetic flight is pitched -30 deg in cruise (truth60.csv), so frames are oblique. Harness hints pass att=(0,0,yaw) (pitch ignored). `s2lib.query_crop_att` resamples the frame onto a north-up ground plane through full attitude (same idea as author's make_queries_oriented.py), centred on the optical-axis ground point; vehicle fix = tile centre minus the known offset. Attitude: roll/pitch from our own complementary filter on imu.csv (`ahrs.py`, median error 0.1-0.2 deg, up to 13 deg in short transients), yaw and baro altitude from the hints.
2. **Streaming fusion.** fuse.py is a batch CLI. `s2core.StreamFuser` re-implements the loop of `causal_mode` step by step, importing `causal_consensus`, `fit_similarity`, `rotz` unchanged from repo/tools/fuse.py. It deliberately omits the CLI's whole-flight consensus pre-filter (non-causal). Re-run from logs it reproduces the adapter output to 8e-9 m.
3. Start position (take-off point) is assumed known (config `start_latlon`); the VIO is anchored there. Before the fusion transform exists (warm-up) the adapter returns the VIO-only estimate with flag `warmup_vio_only` and accepted=True.

## OpenVINS (3 attempts / outcome)
1. Docker daemon was not running; started `dockerd` in background (works, overlayfs). Docker Hub reachable (`ubuntu:20.04`, `ros:foxy-ros-base` pulled).
2. Built the fork (branch alto_integration) in `ros:foxy-ros-base` (Ubuntu 20.04, ROS 2 Foxy; the repo's own Dockerfile uses Galactic and a heavy desktop image, avoided for disk). `colcon build` **succeeded**: ov_core, ov_data, ov_eval, ov_init, ov_msckf, 5 packages in 30 min 21 s (`openvins_gpl/build_summary.txt`). So the "does it build" check passes (last upstream release v2.7 May 2023).
3. The `docker build` image export then failed with ENOSPC (disk was 2.7 GB free; lead asked to prune). I removed the image and cache and did NOT rebuild (another 30 min CPU + ~5 GB). A rosbag writer (`openvins_gpl/make_bag.py`) and NIDAR configs (`openvins_gpl/cfg/`) were prepared but never run. **No OpenVINS trajectory exists**; all VIO numbers are from OUR-VIO-SUBSTITUTE. Risk noted for a future attempt: cruise at constant velocity with a 5 Hz nadir camera and 50 Hz IMU is a weak case for MSCKF initialisation/scale.

## Failed steps (count 7)
1. curl/gh to the forks: 403 (git clone works). 2. `dockerd` killed by my own `timeout 20` -> restarted with nohup. 3. gdown missing import. 4. first stage-2 launch OOM-killed at batch 64 (8 GB) -> batch 32. 5. `exec(train.py)` has no `__file__` -> ran as script. 6. Deleting stageone.pth broke the resume (FileNotFoundError) -> `load_weights_backbone: False`. 7. Docker export ENOSPC.

## Reproducibility verdict
Partly. Code installs and trains with small fixes (gdown, CPU device in configs). No released weights; no tile/VIO scripts for non-MUN-FRL data; fuse.py only runs as a batch CLI on files. Our numbers are reproducible from seed 0 (fuse RANSAC seeded; retrieval deterministic).

## Documentation rubric (0-3)
install steps work as written 1 (requirements incomplete, CUDA defaults in configs, hard-coded author paths); config explained 2 (configs well commented); results reproducible 1 (no checkpoint, depends on author's data/paths); algorithm described 3 (README, fuse.py docstrings are detailed and honest).
