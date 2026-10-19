"""Local pose refinement by render-and-compare edges: maximise image gradient along model depth/normal edges, regularised toward the current pose.
usage: 21_edge_refine.py poses_in.json out.json name1,name2,...   (or ALL_LATE=1541 to do every plain frame >= that index)"""
import sys, os, re, json, time, cv2, numpy as np
from scipy.optimize import minimize
from scipy.spatial.transform import Rotation as Rot
sys.path.insert(0, os.path.dirname(__file__)); from common import *
P = json.load(open(sys.argv[1])); mesh, d = load_model('work/model.npz'); ren = Renderer(mesh, d['OBJ']); S = 1 / 6.
idx = lambda n: int(re.findall(r'\d+', n)[0])
names = sys.argv[3].split(',') if not sys.argv[3].startswith('ALL') else sorted([n for n in P if n.count('_') == 1 and idx(n) >= int(sys.argv[3].split('=')[1])], key=idx)
q = next(iter(P.values()))['params']; K = np.array([[q[0], 0, q[2]], [0, q[1], q[3]], [0, 0, 1.]]); out = dict(P); rep = {}
SR, ST = np.radians(3.0), 0.25
def grad_img(n):
    g = cv2.cvtColor(cv2.imread('frames_all/' + n), cv2.COLOR_BGR2GRAY); g = cv2.undistort(g, K, np.array(q[4:8]), None, K)
    g = cv2.createCLAHE(2.0, (8, 8)).apply(g); g = cv2.resize(g, (int(1920 * S), int(1080 * S)), interpolation=cv2.INTER_AREA).astype(np.float32)
    outs = []
    for sg in (1.5, 4.0):                                              # two blur scales: wide basin first, then sharper
        b = cv2.GaussianBlur(g, (0, 0), sg); G = np.hypot(cv2.Sobel(b, cv2.CV_32F, 1, 0), cv2.Sobel(b, cv2.CV_32F, 0, 1)); outs.append(G / (G.mean() + 1e-3))
    return outs
def edges(R, C):
    dep, tri = ren.render(K, 1920, 1080, R, C, scale=S); hit = tri >= 0; dd = np.log(np.where(np.isfinite(dep), dep, 50.0)); nz = np.where(hit[..., None], mesh.face_normals[np.maximum(tri, 0)], 0)
    e = np.zeros(hit.shape, bool)
    for ax in (0, 1):
        a = [slice(None)] * 2; b = [slice(None)] * 2; a[ax] = slice(1, None); b[ax] = slice(None, -1)
        m = (np.abs(dd[tuple(a)] - dd[tuple(b)]) > 0.07) | (((nz[tuple(a)] * nz[tuple(b)]).sum(-1) < 0.9) & hit[tuple(a)] & hit[tuple(b)]); t = np.zeros(hit.shape, bool); t[tuple(a)] = m; e |= t
    e[:2] = e[-2:] = False; e[:, :2] = e[:, -2:] = False; return e
for n in names:
    p = P[n]; R0, C0 = np.array(p['R_c2w']), np.array(p['C']); Gs = grad_img(n); t0 = time.time()
    def pose(x): return Rot.from_rotvec(x[:3]).as_matrix() @ R0, C0 + x[3:]
    def cost(x, G):
        R, C = pose(x); e = edges(R, C)
        if e.sum() < 25: return 3.0
        ys, xs = np.where(e); return -G[ys, xs].mean() + 0.5 * ((x[:3] / SR) ** 2).sum() / 6 + 0.5 * ((x[3:] / ST) ** 2).sum() / 6
    x = np.zeros(6); c0 = cost(x, Gs[0])
    for G, mf in ((Gs[0], 220), (Gs[1], 220)):
        r = minimize(cost, x, args=(G,), method='Powell', options={'maxfev': mf, 'xtol': 1e-3, 'ftol': 1e-3, 'direction': None}); x = r.x
    c1 = cost(x, Gs[1]); R, C = pose(x); level_ok = True
    rep[n] = {'dt_m': float(np.linalg.norm(x[3:])), 'drot_deg': float(np.degrees(np.linalg.norm(x[:3]))), 'cost_before': float(c0), 'cost_after': float(c1)}
    out[n] = dict(p, R_c2w=R.tolist(), C=C.tolist(), R_c2w_unrefined=R.tolist(), C_unrefined=C.tolist(), edge_refined=True)
    print(n, rep[n], '%.0fs' % (time.time() - t0), flush=True)
json.dump(out, open(sys.argv[2], 'w')); json.dump(rep, open(sys.argv[2] + '.report.json', 'w'))
