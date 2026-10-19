"""Per-frame alignment score: image gradient along model depth/normal edges vs the same edges shifted. ratio>1 => aligned.
usage: 15_edge_score.py poses.json model.npz frames_dir out.json [name1,name2,..]"""
import sys, os, json, numpy as np, cv2
sys.path.insert(0, os.path.dirname(__file__)); from common import *
P = json.load(open(sys.argv[1])); mesh, d = load_model(sys.argv[2]); fdir = sys.argv[3]; ren = Renderer(mesh, d['OBJ']); S = 0.25
names = sys.argv[5].split(',') if len(sys.argv) > 5 else sorted(P); out = {}
for n in names:
    p = P[n]; q = p['params']; K = np.array([[q[0], 0, q[2]], [0, q[1], q[3]], [0, 0, 1.]])
    dep, tri = ren.render(K, 1920, 1080, np.array(p['R_c2w']), np.array(p['C']), scale=S)
    hit = tri >= 0
    if hit.mean() < 0.15: out[n] = {'ratio': 0.0, 'n_edge': 0, 'hit': float(hit.mean())}; continue
    dd = np.where(np.isfinite(dep), dep, 50.0); nz = np.where(hit[..., None], mesh.face_normals[np.maximum(tri, 0)], 0)
    e = np.zeros(hit.shape, bool); ld = np.log(dd)
    for ax in (0, 1):
        sl0 = [slice(None)] * 2; sl1 = [slice(None)] * 2; sl0[ax] = slice(1, None); sl1[ax] = slice(None, -1)
        jump = np.abs(ld[tuple(sl0)] - ld[tuple(sl1)]) > 0.07; ang = (nz[tuple(sl0)] * nz[tuple(sl1)]).sum(-1) < 0.9
        m = jump | (ang & hit[tuple(sl0)] & hit[tuple(sl1)]); tmp = np.zeros(hit.shape, bool); tmp[tuple(sl0)] = m; e |= tmp
    e[:3] = e[-3:] = False; e[:, :3] = e[:, -3:] = False
    if e.sum() < 60: out[n] = {'ratio': 0.0, 'n_edge': int(e.sum()), 'hit': float(hit.mean())}; continue
    img = cv2.imread(os.path.join(fdir, n), cv2.IMREAD_GRAYSCALE); Kc = K.copy(); und = cv2.undistort(img, Kc, np.array(q[4:8]), None, Kc)
    g = cv2.resize(cv2.GaussianBlur(und, (0, 0), 3), (int(1920 * S), int(1080 * S)), interpolation=cv2.INTER_AREA).astype(np.float32)
    gx = cv2.Sobel(g, cv2.CV_32F, 1, 0); gy = cv2.Sobel(g, cv2.CV_32F, 0, 1); G = np.hypot(gx, gy); G = G / (G.mean() + 1e-3)
    G = cv2.dilate(G, np.ones((3, 3), np.uint8))                                        # tolerate 1-px jitter
    ys, xs = np.where(e); s0 = G[ys, xs].mean()
    sh = [G[np.clip(ys + dy, 0, G.shape[0] - 1), np.clip(xs + dx, 0, G.shape[1] - 1)].mean() for dx, dy in ((12, 0), (-12, 0), (0, 12), (0, -12), (20, 0), (-20, 0))]
    out[n] = {'ratio': float(s0 / (np.mean(sh) + 1e-6)), 'n_edge': int(e.sum()), 'hit': float(hit.mean())}
json.dump(out, open(sys.argv[4], 'w'))
for n in names[:40]: print(n, {k: round(v, 2) for k, v in out[n].items()})
