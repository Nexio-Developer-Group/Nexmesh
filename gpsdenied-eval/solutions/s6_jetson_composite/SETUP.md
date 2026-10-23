# S6 "our composite" -- setup log

"Our composite" is NOT an existing project. It is a Jetson-oriented baseline assembled by us from permissive parts:
MegaLoc (retrieval) + FAISS tile index + XFeat/LighterGlue (matching) + planar PnP + consistency gate + tracking window.
All data below is synthetic NIDAR-like. The map used for retrieval is the SAME map the video was rendered from (optimistic appearance gap).

Host: Intel Xeon 2.10 GHz, 4 cores shared with other agents (load avg 7-9 most of the time), 15 GB RAM, no GPU, Python 3.12 (uv venv; system python is 3.13).

## Commands that worked (from `solutions/s6_jetson_composite`)
```
export PIP_CACHE_DIR=/tmp/pipcache
uv venv --python 3.12 env && source env/bin/activate
uv pip install torch torchvision --index-url https://download.pytorch.org/whl/cpu      # torch 2.14.1+cpu
uv pip install numpy opencv-python-headless faiss-cpu pycolmap onnx onnxruntime timm huggingface_hub pandas scipy kornia einops tqdm pillow safetensors
mkdir repo && cd repo
git clone --depth 1 https://github.com/verlab/accelerated_features   # XFeat + LighterGlue weights live in the repo (weights/)
git clone --depth 1 https://github.com/gmberton/MegaLoc
git clone --depth 1 https://github.com/cvg/LightGlue && uv pip install -e LightGlue
uv pip uninstall opencv-python opencv-python-headless && uv pip install opencv-python-headless   # see failure 3
# MegaLoc weights (HF gberton/MegaLoc, model.safetensors, 0.87 GB) download on first load into repo/hf
cd .. && python driver.py --run-id S6-T1-20261023-s0 --stride 1
```
Pins: COMMIT.txt (MegaLoc 5fe0dd6, accelerated_features e92685f, LightGlue eb42fee), lock.txt (pip freeze).
Wall clock from clone to first working output: about 95 min (of which ~18 min MegaLoc index build under CPU contention, ~45 min tracking down the camera-geometry problem below). Failed steps: 6.

## Failures and fixes
1. `torch.hub.load("gmberton/MegaLoc", ...)` -> `HTTP Error 400: Bad Request` (torch.hub fetches the GitHub zip/branch via api.github.com, not reachable through the proxy). Fix: `git clone` (works) and `torch.hub.load(<local clone>, "get_trained_model", source="local")`; this still uses MegaLoc's own hubconf and downloads weights from Hugging Face (reachable). Equivalent code path, different source.
2. `pip install lightglue` -> not on PyPI under that name. Fix: install from the git clone (cvg/LightGlue).
3. Installing LightGlue pulled `opencv-python` which shadows `opencv-python-headless` (twice, also after moving the clone). Fix: uninstall both, reinstall headless.
4. `uv pip install -e` pointed at a path I later moved (/tmp -> repo/); reinstalled from the final path.
5. TensorRT: `pip install tensorrt` -> sdist build aborts, "nvidia-smi command not found". `tensorrt-cu12==10.3.0` -> same build error (needs NVIDIA index + driver). `--no-deps tensorrt-cu12-libs` -> wheel extraction `No space left on device` (disk was shared and full). Three attempts, all failed; removed. No FP16 engine can be built without a GPU anyway.
6. Disk: uv cache reached 4 GB; purged (`uv cache clean`) after lead's notice. Env 1.6 GB, repo (incl. 0.87 GB weights) 1 GB.
7. Own mistakes: a `pkill -f driver.py` killed my own shell; deleting the symlink frame dir of a running job broke it (restarted).

## Data defects found (reported to lead)
- Original frames were rendered with a body-fixed camera while the quad pitches -30 deg in cruise (truth60 pitch -0.52 rad) -> strongly oblique. Re-rendered by lead as gimbal nadir. NOTE: truth60.csv still contains the old pitch; our final runs use `att_source=hints` (roll=pitch=0) and ignore truth60.
- Harness hint yaw is in degrees (adapter converts). run_replay sorts file names as strings; our driver symlinks frames with zero-padded names.

## Licences
| Component | Licence | Notes |
|---|---|---|
| MegaLoc code + weights (gberton, HF gberton/MegaLoc) | MIT (repo LICENSE); weights trained on public VPR datasets - weight licence not separately stated | |
| XFeat code (verlab/accelerated_features) | Apache-2.0 | |
| XFeat weights `xfeat.pt` | repo says Apache-2.0 (single repo licence) | |
| LighterGlue weights `xfeat-lighterglue.pt` | NOT separately confirmed; repo licence Apache-2.0, but derives from LightGlue (Apache-2.0 code; SuperPoint-trained LightGlue weights carry non-commercial terms) -> treat as unconfirmed | record |
| LightGlue (cvg) / SuperPoint weights (used only in T3 swap) | LightGlue Apache-2.0; SuperPoint weights (MagicLeap) research-only licence | not for product |
| DINOv2-small (timm, T2 swap) | Apache-2.0 | |
| hloc (structure only, no code used) | BSD-3 | |
| S1 gnss-denied-nav (ideas only) | MIT | |
| FAISS | MIT; PyTorch BSD; OpenCV Apache-2.0; kornia Apache-2.0; onnxruntime MIT | |

## Reproducibility verdict
Reproducible given the commit hashes, lock.txt and seeds (no stochastic components apart from OpenCV RANSAC, which is not seeded by cv2 -> bit-exact reproduction not guaranteed; seed arg is recorded). Index build is cached in repo/cache (not committed).

## Documentation-quality rubric (0-3)
- Install steps work as written (ours, composite of 3 READMEs): 2 (torch.hub path fails behind this proxy; opencv conflict)
- Config explained: 3 (s6lib.DEFAULT is commented, every key used)
- Results reproducible: 2 (RANSAC unseeded, latency depends on shared load)
- Algorithm described: 3 (ANALYSIS.md written from the code)
