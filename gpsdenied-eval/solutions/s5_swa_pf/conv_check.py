import sys, os, numpy as np, cv2
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__))); import adapter, run_replay
from PIL import Image
D = adapter.HERE + '/../../data/nidar'
a = adapter.Adapter(); a.init(D + '/map_esri_z19.png', dict(n=1000, res=1.0)); pf = a.pf; pf.remap = lambda: None
tr = run_replay.load_truth(D + '/truth.csv'); os.chdir(adapter.REPO)
variants = {f'{s:+d}yaw+{o}': (s, o) for s in (1, -1) for o in (0, 90, 180, 270)}
res_ = {k: [] for k in variants}
for t in [10, 30, 45, 70, 110, 130]:
    i = int(np.argmin(abs(tr['t'] - t)))
    X, Y = a.ref.latlon_to_px(tr['lat'][i], tr['lon'][i]); cx, cy = (Y - a.Y0) * a.gm, (X - a.X0) * a.gm
    fr = cv2.imread(f'{D}/frames/{t:.3f}.png'); _, pr = a.seg.detect_image(Image.fromarray(fr[:, :, ::-1])); pr = np.where(pr == 5, 1, pr)
    g = np.arange(-100, 101, 20); gx, gy = np.meshgrid(g, g, indexing='ij'); n = gx.size
    for k, (s, o) in variants.items():
        P = np.zeros((n, 6), np.float32); P[:, 0] = cx + gx.ravel(); P[:, 1] = cy + gy.ravel(); P[:, 2] = tr['alt'][i]; P[:, 3] = (s * tr['yaw'][i] + o) % 360
        pf.particles = P; pf.num_particles = n; pf.update_particles_fast(pr)
        w = pf.particles[:, 4]; res_[k].append((int((w > w[n // 2]).sum()), float(w.max())))
for k, v in res_.items(): print(k, 'rank of truth (of 121):', [r for r, _ in v], 'wmax mean %.2e' % np.mean([m for _, m in v]))
