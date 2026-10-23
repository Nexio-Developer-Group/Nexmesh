# S1 setup: koksalan/gnss-denied-nav (commit 572b9ffe..., see COMMIT.txt)

CPU: Intel Xeon @ 2.10 GHz, 4 cores shared (we used 2 threads, `nice`), 15 GB RAM, no GPU. Python 3.12 (uv venv; system python is 3.13).
Wall-clock from clone to first working output (frame 0 localised to 1.0 m): about 25 min (of which 15 min is the one-off tile index build). Total agent time including all tests: roughly 9 h (CPU-bound). Failed or changed steps: 6 (listed below).

## Commands that worked
```
export UV_NO_CACHE=1                       # disk is tight (cleaned 2 GB uv cache afterwards)
cd solutions/s1_gnss_denied_nav
uv venv --python 3.12 env && . env/bin/activate
uv pip install torch torchvision --index-url https://download.pytorch.org/whl/cpu     # torch 2.14.1+cpu
uv pip install "transformers>=4.45" faiss-cpu rasterio opencv-python-headless numpy scipy pandas matplotlib tqdm \
   kornia timm onnx onnxruntime scikit-learn joblib pymavlink imageio "lightglue @ git+https://github.com/cvg/LightGlue.git"
uv pip install onnxscript                  # only for the LightGlue ONNX (dynamo) export attempt
uv pip freeze > lock.txt                   # 74 packages
# first run builds + caches the tile DB and map keypoints (15 min), later runs start in ~20-40 s:
OMP_NUM_THREADS=2 nice python solutions/s1_gnss_denied_nav/driver.py --id S1-T1 --stride 5 --config '{"query_kp":1024}'
```
Skipped on purpose (out of scope): ultralytics/YOLO, stable-baselines3, gymnasium, fastapi/uvicorn (detection, RL planner, ground station). Not needed for localisation.

## Weights (DEGRADED)
* Retriever: the repo has NO released fine-tuned weights (`outputs/` is git-ignored; README only shows training commands that need UAV-VisLoc, hosted on Baidu). Training was not feasible: no VisLoc data on disk (a 17.7 GB HF mirror `haiduonghuynhle/UAV_VisLoc_dataset` exists but exceeds the disk allowance; free disk was ~2.7-13 GB) and CPU fine-tuning of DINOv2-B would take far longer than 45 min.
* So: **pretrained `facebook/dinov2-base` (HF, 86.6 M params, 346 MB fp32 ONNX / ~330 MB HF cache), pool `cls+gem`, no fine-tune. All S1 results are flagged DEGRADED.** The author's own table (README) gives pretrained-DINOv2-B top-1 retrieval of only 18.2 % vs the fine-tuned model; we reproduce that weakness.
* SuperPoint `superpoint_v1.pth` (5 MB) and LightGlue `superpoint_lightglue` (45 MB) are downloaded by the `lightglue` package from github.com/cvg/LightGlue releases.

## Index build (map data/nidar/map_esri_z19.png), recorded in cache/build_*.json
* Map resampled once from Web-Mercator to a linear lat/lon grid in RAM (the repo requires an EPSG:4326 GeoTIFF; `MapShim` in adapter.py replaces `SatelliteMap`). Native 0.283 m/px kept; the repo itself resamples to 0.625 m/px (retrieval tiles, 224 px) and 0.5 m/px (SuperPoint keypoint map) and warps each camera frame to the same scale, so no resampling to the camera GSD (0.18 m/px) is needed. Ratio map/camera = 1.57; camera -> 0.5 m/px is a 2.77x down-scale.
* 1225 tiles (140 m patch, 50 m stride), 1536-d descriptors (cls+gem), descriptor DB 7.5 MB. Build time 685 s (DINOv2-B on CPU, 0.56 s/tile under load). The onboard localizer does exact dot-product search with `torch` (`db @ q`), NOT FAISS; FAISS (`IndexFlatIP`, same exact search) appears only in `gdnav/evaluation.py` and `scripts/eval_int8.py`. README's "FAISS index" is therefore only partly accurate.
* SuperPoint keypoints of the whole map precomputed once: 4970x4970 px image at 0.5 m/px, 47 958 keypoints (up to 2600 per 1024 px tile), 124 MB in RAM, build 202 s.
* Peak RSS of a replay process ~2.4-3 GB.

## Camera config (camera.json, repo format of configs/camera_visloc*.json)
`focal_px` 1108.51, `heading_sign` +1, `heading_offset_deg` 0 (the video has image-up = heading; verified: matches succeed at 133-172 inliers with 1 m error on the first leg; the repo's VisLoc configs use sign -1 for their dataset). `patch_m` = 140 m (the repo default 250 m would exceed the inscribed circle of our 1280x800 frame at 200 m: 800 px x 0.18 m = 144 m).

## Failures / deviations (each counted)
1. `SatelliteMap` needs an EPSG:4326 GeoTIFF -> wrote an in-memory shim (no GeoTIFF re-reads, as README recommends).
2. Repo default patch 250 m does not fit our camera -> 140 m.
3. CPU cost: stock config (top-10 rerank, 2048 query keypoints, 450 m windows) took 35-60 s/frame at the beginning (contended box; SuperPoint upsamples the 280 px query to 1024 px; ten LightGlue runs ~3 s each) -> `query_kp=1024` for all runs (calibration runs in results/s1/nidar_oblique/ and S1-calib-nadir; on oblique-data calibration frames 2048 vs 1024 keypoints gave 1.0 vs 1.5 m on frame 0 and the 2048 run died before finishing, so equivalence is only weakly checked). Lower `search_m=250` broke correct matches (inliers 11 vs 37), rejected.
4. harness/run_replay.py sorts frame names as strings (100.0 before 20.2) -> driver sorts numerically; yaw hint is in degrees (adapter has `yaw_unit`).
5. First data set (body-fixed, 30 deg oblique camera) replaced by the lead with a nadir render after ~3 h of my runs; those runs were stopped and not used (calibration logs kept in results/s1/nidar_oblique/).
6. A calibration process (2048 keypoints) died (probably OOM while other agents were running); rerun not needed.

## Reproducibility verdict
Setup with the repo as published: reproducible for localisation code; **results of the author are not reproducible** because weights, training data and dataset links are not published (Baidu only), and no pinned requirements (requirements.txt has only lower bounds such as `torch>=2.4`; `lightglue` from git HEAD). Seeds: no stochastic component in inference except RANSAC (OpenCV, default seeding); `--seed 0` set for numpy/torch, T9 frame-drop uses numpy rng seed 0.

## Licences
| component | licence |
|---|---|
| koksalan/gnss-denied-nav | MIT |
| DINOv2 (facebook/dinov2-base) | Apache-2.0 |
| LightGlue (cvg) code | Apache-2.0; its SuperPoint-LightGlue weights follow the SuperPoint licence |
| SuperPoint (Magic Leap) weights | Magic Leap research licence (non-commercial) |
| PyTorch / torchvision | BSD-3 |
| transformers | Apache-2.0 |
| faiss-cpu | MIT |
| OpenCV (headless) | Apache-2.0 |
| ONNX Runtime / onnx | MIT / Apache-2.0 |
| rasterio, kornia, timm | BSD-3 / Apache-2.0 / Apache-2.0 |
| UAV-VisLoc dataset (not used) | CC BY-NC-SA (per dataset page; verify) |
| Esri World Imagery map (our benchmark map) | Esri terms, benchmark only, not redistributed |
Note: the SuperPoint non-commercial licence is a blocker for commercial deployment.

## Documentation rubric (0-3)
* Install steps work as written: **1** (requirements.txt has no versions/CPU path; README has no install section for the localizer; GeoTIFF/EPSG:4326 requirement implicit; weights absent).
* Config explained: **2** (camera json + LocalizerConfig docstrings; thresholds explained in code comments, but `patch_m` vs camera footprint is not documented).
* Results reproducible: **1** (scripts exist, but data/weights unobtainable).
* Algorithm described: **3** (README + module docstrings are detailed and match the code).
