"""Check update_particles_fast == repo update_particles (weights) on one NIDAR frame, 40 particles."""
import sys, os, numpy as np, cv2, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__))); import adapter
from PIL import Image
D = adapter.HERE + '/../../data/nidar'
a = adapter.Adapter(); a.init(D + '/map_esri_z19.png', dict(n=1000, seed=3))
fr = cv2.imread(D + '/frames/20.000.png'); _, pr = a.seg.detect_image(Image.fromarray(fr[:, :, ::-1])); pr = np.where(pr == 5, 1, pr)
pf = a.pf; pf.particles = pf.particles[:40].copy(); pf.num_particles = 40
pf.remap = lambda: None   # neutralise resampling for the comparison
os.chdir(adapter.REPO)
P0 = pf.particles.copy(); t = time.time(); pf.update_particles(pr); w1 = pf.particles[:, 4].copy(); t1 = time.time() - t
pf.particles = P0.copy(); t = time.time(); pf.update_particles_fast(pr); w2 = pf.particles[:, 4].copy(); t2 = time.time() - t
print('repo s', t1, 'fast s', t2); print('max rel diff', np.max(np.abs(w1 - w2) / (np.abs(w1) + 1e-30))); print(w1[:5], w2[:5])
