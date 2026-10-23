"""Sanity check of coordinate/yaw conventions: weight landscape around the TRUE pose (diagnostic only, uses ground truth on purpose)."""
import sys, os, numpy as np, cv2, json
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__))); import adapter, run_replay
from PIL import Image
D = adapter.HERE + '/../../data/nidar'
res = float(sys.argv[1]) if len(sys.argv) > 1 else 1.0
a = adapter.Adapter(); a.init(D + '/map_esri_z19.png', dict(n=1000, res=res)); pf = a.pf; pf.remap = lambda: None
tr = run_replay.load_truth(D + '/truth.csv'); out = []
os.chdir(adapter.REPO)
for t in [10, 30, 45, 70, 90, 110, 130, 150]:
    i = int(np.argmin(abs(tr['t'] - t)))
    X, Y = a.ref.latlon_to_px(tr['lat'][i], tr['lon'][i]); cx, cy = (Y - a.Y0) * a.gm / res, (X - a.X0) * a.gm / res
    fr = cv2.imread(f'{D}/frames/{t:.3f}.png'); _, pr = a.seg.detect_image(Image.fromarray(fr[:, :, ::-1])); pr = np.where(pr == 5, 1, pr)
    g = np.arange(-150, 151, 10) / res
    gx, gy = np.meshgrid(g, g, indexing='ij'); n = gx.size
    P = np.zeros((n, 6), np.float32); P[:, 0] = cx + gx.ravel(); P[:, 1] = cy + gy.ravel(); P[:, 2] = tr['alt'][i]; P[:, 3] = tr['yaw'][i]
    pf.particles = P; pf.num_particles = n; pf.update_particles_fast(pr)
    w = pf.particles[:, 4]; k = int(np.argmax(w)); off = (gx.ravel()[k] * res, gy.ravel()[k] * res)
    out.append(dict(t=t, argmax_offset_north_down_m=off, w_at_truth=float(w[n // 2]), w_max=float(w.max()), rank_of_truth=int((w > w[n // 2]).sum())))
    print(out[-1], flush=True)
json.dump(out, open(adapter.HERE + f'/../../results/s5/nidar/align_check_res{res}.json', 'w'), indent=1)
