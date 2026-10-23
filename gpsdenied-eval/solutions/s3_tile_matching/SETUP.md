# S3 setup log (ShuhaievaPolina/UAV-Visual-Localization-via-Satellite-Imagery)

Repo commit: see `COMMIT.txt` (e63334eda6359391fc9f3700671aac1dbac6f342). Versions: `lock.txt` (torch 2.14.1+cpu, timm 1.0.30, lightglue from git, rasterio 1.5.2, numpy 2.5.2, Python 3.13). All results are **synthetic NIDAR-like** data.

Wall clock from clone to first working output: about 45 min (of which 11 min is the one-off DINOv2 tile-embedding build, 361 tiles, on a loaded CPU). Failed or changed steps: 3.

## Commands that worked
```
python3 -m venv env; export PIP_CACHE_DIR=/tmp/pipcache
env/bin/pip install torch torchvision --index-url https://download.pytorch.org/whl/cpu
env/bin/pip install timm opencv-python-headless rasterio numpy pandas matplotlib pillow tqdm "git+https://github.com/cvg/LightGlue.git"
env/bin/python run_s3.py --tag S3-T1 --step 5            # offline replay, 1 Hz, as published (no hints)
env/bin/python run_s3.py --tag S3-T1b --step 5 --derotate # OUR extension: de-rotate frames north-up with the compass yaw
```
`adapter.py` re-implements the notebook's classes (`GeoCalibrator`, `SatelliteTileDatabase`, `EdgeLightGlueMatcher`, `run_evaluation_pipeline`) one-to-one (1024 px tiles, 512 stride, DINOv2-small `vit_small_patch14_dinov2.lvd142m`, top-5, sim threshold 0.82, up to 3 candidates, early stop at 25 inliers, USAC_MAGSAC at 5 px, ≥6 inliers else top-1 tile-centre fallback). The map is first resampled so one pixel equals the camera ground resolution (ratio 1.569 = 0.283 / 0.1804 m/px; recorded in `metrics.json` as `scale_ratio`).

## Failures and fixes
1. **The project is a Colab notebook, not a runnable package.** It mounts Google Drive for the UAV-VisLoc zip and uses `!pip`. Fix: the class code was transplanted into `adapter.py` (a reimplementation of the same logic; nothing else changed).
2. **ALIKED weights download returned HTTP 403** (`github.com/.../raw/main/models/aliked-n16.pth` through the proxy). Fix: the same file from `raw.githubusercontent.com` (clone of Shiaoming/ALIKED) copied into the torch hub cache. The weights were later deleted by another job's cache clean-up and restored the same way.
3. **Our own adapter bug**: a duplicate `derotate` keyword in the config dict and a sign error in the de-rotation angle (fixed before the reported runs).
Not a failure but a trap: the notebook feeds the drone image **unrotated**, the query images of its dataset are north-up; frames from a heading-up camera are rotated by the vehicle heading and the pipeline has no rotation handling.

## Reproducibility verdict
Partly reproducible. The code is short and the algorithm is described, but there is no script, no config file, no pinned requirements (`requirements.txt` has no versions and lists `torch` without CPU/GPU guidance), no seeds other than `seed_everything(42)`, and the dataset needs a Google-Drive archive. Our runs are reproducible from the commands above (no stochastic parts apart from OpenCV's RANSAC).

## Licences (internal evaluation only; nothing redistributed)
| Component | Licence |
|---|---|
| S3 repository (code + notebook) | **none declared** (no LICENSE file; all rights reserved by default) |
| DINOv2 (`timm` weights `vit_small_patch14_dinov2.lvd142m`) | Apache-2.0 |
| ALIKED (Shiaoming/ALIKED) | BSD-3-Clause (weights from the same repository; not independently verified) |
| LightGlue (cvg) | Apache-2.0 code; the `aliked_lightglue` weights licence is not separately stated |
| timm, PyTorch, OpenCV, rasterio | Apache-2.0 / BSD |
| Esri World_Imagery map | benchmark use only |

## Documentation-quality rubric (0-3)
- Install steps work as written: **1** (`pip install -r requirements.txt` works; but the run instructions are "open the notebook in Colab" and one weight download fails behind a proxy).
- Config explained: **1** (parameters are stated in the README but there is no config file; `MIN_INLIERS` and the 0.82 similarity threshold are only in code).
- Results reproducible: **1** (a predictions CSV is given; the dataset archive is not available here).
- Algorithm described: **3** (the README has a clear pipeline diagram and parameter list that match the code).
Total 6/12.
