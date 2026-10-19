"""Variance-of-Laplacian blur filter. usage: 02_blur_filter.py frames_dir out_dir [keep_ratio_vs_local_median]"""
import cv2, sys, os, json, numpy as np, shutil
src, dst = sys.argv[1], sys.argv[2]
ratio = float(sys.argv[3]) if len(sys.argv) > 3 else 0.6
os.makedirs(dst, exist_ok=True)
files = sorted(f for f in os.listdir(src) if f.endswith('.jpg'))
vals = []
for f in files:
    g = cv2.imread(os.path.join(src, f), cv2.IMREAD_GRAYSCALE)
    g = cv2.resize(g, (960, 540), interpolation=cv2.INTER_AREA)
    vals.append(float(cv2.Laplacian(g, cv2.CV_64F).var()))
vals = np.array(vals)
# local (+-15 frame) median so slow content changes (textureless walls) are not mistaken for blur
med = np.array([np.median(vals[max(0, i - 15):i + 16]) for i in range(len(vals))])
keep = (vals >= ratio * med) & (vals >= 0.15 * np.median(vals))
for f, k in zip(files, keep):
    if k:
        os.symlink(os.path.abspath(os.path.join(src, f)), os.path.join(dst, f))
json.dump({'files': files, 'lapvar': vals.tolist(), 'keep': keep.tolist()}, open(os.path.join(dst, '..', 'blur_stats.json'), 'w'))
print(f"total {len(files)} kept {int(keep.sum())} dropped {int((~keep).sum())}; lapvar median {np.median(vals):.1f} min {vals.min():.1f} max {vals.max():.1f}")
