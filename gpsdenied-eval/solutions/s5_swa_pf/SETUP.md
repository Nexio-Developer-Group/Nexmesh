# S5 SWA-PF: SETUP (CPU only, 4 shared cores, ~13 GB shared disk)

Repo: YuanJiayuuu/SWA-PF @ e8a92173fb90fa44c2d472a7ebd6819e99bbc186 (repo/ untouched; all changes are adapter-side).
Wall-clock from clone to first working output (a SegFormer label map of a NIDAR frame): about 25 min (env 8 min incl. 2 disk-full retries, code reading the rest). Failed steps: 3 (list below). First end-to-end filter step: about 45 min (slow-step fix needed, see F3).
Dataset MAFS: NOT obtainable (see ../../results/s5/mafs/MAFS_unavailable.md). Demo (`python SWAPF.py`) cannot run as-is: needs MAFS at hard-coded `D:/DATASETS/MAFS`, a GPU-only `SegFormer_Segmentation` (cuda=True default; DataParallel .cuda()) and `cv2.imshow`/windows.

## Commands that worked
```
export PIP_CACHE_DIR=/tmp/pipcache UV_NO_CACHE=1
cd solutions/s5_swa_pf
/root/.local/bin/uv venv --python 3.9 env          # CPython 3.9.25 (README: 3.9, OK as written)
source env/bin/activate
uv pip install --no-cache --extra-index-url https://download.pytorch.org/whl/cpu "torch==1.10.0+cpu" "torchvision==0.11.1+cpu"
uv pip install --no-cache "numpy==1.26.4" opencv-python-headless pandas scikit-learn matplotlib timm einops scipy "setuptools<70"
OMP_NUM_THREADS=2 nice python build_semmap.py        # substitute semantic map (1 min)
OMP_NUM_THREADS=2 nice python run_s5.py --run-id S5-T2-20261023-s0 --n 20000 --stride 5     # as-is replay, 1 Hz
OMP_NUM_THREADS=2 nice python run_s5.py --run-id S5-T2b-20261023-s0 --rectify --n 20000 --stride 5   # attitude-rectified extension
```
Versions: see lock.txt (49 packages): torch 1.10.0+cpu, torchvision 0.11.1+cpu (the exact versions the README names, CPU builds from download.pytorch.org/whl/cpu: still available), numpy 1.26.4, opencv-python-headless 4.11.0.86, scikit-learn 1.6.1, timm 1.0.30, Python 3.9.25. CPU: Intel Xeon 2.10 GHz (4 vCPU, load 9-11 from other agents). Env size 1.3 GB.
Seeds: adapter seed config (default 0): numpy/torch/python RNG + numpy Generator for init and motion noise; odometry noise generator seeded 1000+seed.

## Failures and fixes
- F1 `uv pip install torch` ENOSPC (shared disk full): removed uv cache, `--no-cache`, TMPDIR inside project. Retried twice.
- F2 `import torch` -> `No module named pkg_resources` (torch 1.10 needs it; new setuptools dropped it): `setuptools<70`.
- F3 Repo `update_particles` is a python loop of per-particle torch ops (4x400x400 float64 products): 35 s per frame for 2,000 particles under load (measured, dbg run), about 6 min per frame for 20,000. Fix (adapter side): `State.update_particles_fast`, an exact rewrite of the same weight formula using an integral image (verify_fast.py: max relative weight difference 1.3e-3 from nearest-neighbour block-boundary rounding). 20,000 particles then cost about 35 s per frame, 1,000 about 5 s (rotation set-up dominates).
- F4 `SegFormer_Segmentation` hard-codes `.cuda()`: constructed with `cuda=False` (config option exists, no repo edit). Loads state_dict with map_location=cpu: works. 2 threads, 512x512 input: about 1.5-2 s per frame on the loaded CPU.
- F5 `ParticleState.__init__` wants files `label.png`, `img.png` and `detail.txt`-derived list: adapter writes a temporary label PNG and a 4x4 dummy img.png (img only feeds an unused grayscale copy), passes `map_detail=[x_min,x_max,y_min,y_max,height_to_pix,...]` in local metres. Init mode is a local variable in `__init__` (cannot be selected from outside): adapter re-initialises particles itself with the three repo modes.
- Diagnosed late (not a setup failure but cost 1 h): the camera is body-fixed and SITL cruise pitch is -30 deg, so frame centre lies about 115 m behind the drone (see ANALYSIS). Repo (and paper) estimate x,y,altitude,yaw only.

## Reproducibility verdict
Install steps work as written for the README's versions (CPU builds instead of cu113). Reproducing the paper numbers is impossible here (dataset on Baidu Pan only, partial). Demo script is not runnable without edits (hard-coded Windows path, GPU, imshow). Weights shipped in repo (logs/8_b0, 15 MB, sha256 prefix 8de15426fbd2b4f4); the satellite U-Net weights for map labelling are NOT shipped (no code for the U-Net in repo besides the SegFormer; map `label.png` is expected from the dataset).

## Licences
| Component | Licence |
|---|---|
| SWA-PF code (YuanJiayuuu/SWA-PF) | none declared (no LICENSE file, README silent) |
| SegFormer-B0 weights `logs/8_b0` (trained by authors, MAFS-derived) | none declared |
| MAFS dataset | none declared (Baidu Pan) |
| PyTorch / torchvision | BSD-3 ; numpy BSD ; OpenCV Apache-2.0 ; scikit-learn BSD ; timm Apache-2.0 |
| Esri World_Imagery map (our benchmark map) | benchmark only, not redistributed |
Internal evaluation only; nothing redistributed.

## Documentation quality (0-3)
- Install steps work as written: 2 (versions exist; Windows wheel names; no pkg_resources/disk notes)
- Config explained: 1 (parameters like cam_view, particle_detail, height_to_pix undocumented; detail.txt format only inferable from code)
- Results reproducible: 0 (dataset not downloadable here; no result scripts; demo uses ground-truth pose)
- Algorithm described: 2 (paper good; code has Chinese comments, dead code, hard-coded modes)
