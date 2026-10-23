"""Training tiles for CAEVL from the NIDAR reference map only (no frames, no truth).
Stage-1/2 train on clean reference tiles; degradation is applied by CAEVL's own augmentations."""
import sys, os, pickle, numpy as np, cv2
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from s2lib import *
img, ref, mpp = load_map()
rng = np.random.default_rng(0)
out = os.path.join(HERE, 'work/train'); coords = {}
N = {'train': int(sys.argv[1]) if len(sys.argv) > 1 else 1800, 'val': 240}
for split, n in N.items():
    d = os.path.join(out, split); os.makedirs(d, exist_ok=True)
    for i in range(n):
        T = rng.choice([60, 120, 240]); half = T / mpp / 2
        cx = rng.uniform(half, img.shape[1] - half); cy = rng.uniform(half, img.shape[0] - half)
        name = f'{split}_{i:05d}_{T}.png'
        cv2.imwrite(os.path.join(d, name), cut_tile(img, cx, cy, T, mpp)); coords[name] = (float(cx * mpp), float(cy * mpp))
pickle.dump(coords, open(os.path.join(out, 'coords.pkl'), 'wb'))
print('tiles', {k: len(os.listdir(os.path.join(out, k))) for k in N})
