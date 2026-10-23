"""OUR SUBSTITUTE SEMANTIC MAP (not the paper's U-Net labelling).
Runs the repo's drone SegFormer-B0 (logs/8_b0) over data/nidar/map_esri_z19.png in 1280x800 windows
resampled to 0.18 m/px (same scale/size as the NIDAR frames), writes label mosaic at 0.5 m/px for the search area
and majority-pooled 1 m/px and 2 m/px versions. Labels use the segmenter's 7 ids (0 ground,1 wall/bldg,2 road,3 veg,4 vehicle,5 roof,6 water)
with 5->1 applied later (as SWAPF.py does)."""
import os, sys, json, time
import numpy as np, cv2, torch
ROOT = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, ROOT + '/repo'); sys.path.insert(0, ROOT + '/../../harness')
torch.set_num_threads(2)
from PIL import Image
import geo
os.chdir(ROOT + '/repo')
from segformer import SegFormer_Segmentation
D = ROOT + '/../../data/nidar'
ref = geo.MapRef(D + '/map_esri_z19.json'); gm = ref.ground_m_per_px(18.52)
big = cv2.imread(D + '/map_esri_z19.png')
X0, X1, Y0, Y1 = 1470, 4635, 2010, 5180   # search area in map px (path bbox +~200 m margin)
seg = SegFormer_Segmentation(cuda=False)
FOOT_W, FOOT_H = 1280 * 0.18, 800 * 0.18  # m
OUT = 0.5
wpx, hpx = int(round(FOOT_W / gm)), int(round(FOOT_H / gm))
ow, oh = int(round(FOOT_W / OUT)), int(round(FOOT_H / OUT))
nx, ny = int(np.ceil((X1 - X0) / wpx)), int(np.ceil((Y1 - Y0) / hpx))
mosaic = np.zeros((ny * oh, nx * ow), np.uint8)
t0 = time.time()
for j in range(ny):
    for i in range(nx):
        x, y = X0 + i * wpx, Y0 + j * hpx
        win = big[y:y + hpx, x:x + wpx]
        win = cv2.resize(win, (1280, 800), interpolation=cv2.INTER_CUBIC)       # -> 0.18 m/px
        g = cv2.cvtColor(cv2.cvtColor(win, cv2.COLOR_BGR2GRAY), cv2.COLOR_GRAY2BGR)  # frames are gray
        _, pr = seg.detect_image(Image.fromarray(g[:, :, ::-1]))
        mosaic[j * oh:(j + 1) * oh, i * ow:(i + 1) * ow] = cv2.resize(pr.astype(np.uint8), (ow, oh), interpolation=cv2.INTER_NEAREST)
    print(j, ny, round(time.time() - t0), 's', flush=True)
def pool(m, k):
    h, w = (m.shape[0] // k) * k, (m.shape[1] // k) * k
    b = m[:h, :w].reshape(h // k, k, w // k, k).transpose(0, 2, 1, 3).reshape(h // k, w // k, k * k)
    cnt = np.stack([(b == c).sum(-1) for c in range(7)], -1)
    return cnt.argmax(-1).astype(np.uint8)
meta = dict(x0_px=X0, y0_px=Y0, gm=gm, windows=[nx, ny])
for res, k in ((0.5, 1), (1.0, 2), (2.0, 4)):
    m = mosaic if k == 1 else pool(mosaic, k)
    np.save(f'{ROOT}/work/semmap_{res}.npy', m)
    meta[str(res)] = dict(shape=list(m.shape), hist=(np.bincount(m.ravel(), minlength=7) / m.size).round(4).tolist())
json.dump(meta, open(f'{ROOT}/work/semmap_meta.json', 'w'), indent=1)
print(meta, time.time() - t0)
