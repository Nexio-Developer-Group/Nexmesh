"""Align SfM fragments with no visual link to the anchored model, using only model geometry.
gravity from camera roll~0 -> 2D (yaw, scale, tx, ty) FFT chamfer search vs model wall traces -> 3D refine.
usage: 06_align_fragments.py sparse_dir_of_fragment model.npz out.json"""
import sys, json, numpy as np, pycolmap, cv2, time
from scipy.signal import fftconvolve
from scipy.optimize import least_squares
from scipy.spatial.transform import Rotation as Rot
sys.path.insert(0, __import__('os').path.dirname(__file__)); from common import *
rec = pycolmap.Reconstruction(sys.argv[1]); mesh, d = load_model(sys.argv[2]); cams = recon_cams(rec)
RES = 0.1; V = mesh.vertices; F = mesh.faces
lo = V[:, :2].min(0) - 25; hi = V[:, :2].max(0) + 25; Wg, Hg = np.ceil((hi - lo) / RES).astype(int)
img = np.zeros((Hg, Wg), np.uint8)
n = mesh.face_normals
for f, nn in zip(F, n):
    if abs(nn[2]) < 0.05:
        p = V[f][:, :2]; a, b = p[np.argmin(p[:, 0] * 0 + 0)], p
        # draw all triangle edges that are non-degenerate in XY
        for i in range(3):
            q0, q1 = p[i], p[(i + 1) % 3]
            if np.linalg.norm(q0 - q1) > 1e-3:
                cv2.line(img, tuple(((q0 - lo) / RES).round().astype(int)), tuple(((q1 - lo) / RES).round().astype(int)), 255, 1)
DT = cv2.distanceTransform(255 - img, cv2.DIST_L2, 3) * RES; TAU = 0.5; Fimg = (-np.exp(-(DT / 0.45) ** 2)).astype(np.float32)   # soft inlier kernel (negated: lower = better)
# gravity: up u with u.right_i ~ 0
rights = np.array([(-R.T @ np.zeros(3), R.T[:, 0])[1] for R, t, c in cams.values()]); downs = np.array([R.T[:, 1] for R, t, c in cams.values()])
u = np.linalg.svd(rights, full_matrices=False)[2][-1]; 
if u @ downs.mean(0) > 0: u = -u
zax = u; xax = np.cross([0, 1., 0], zax); xax /= np.linalg.norm(xax); yax = np.cross(zax, xax); Rg = np.stack([xax, yax, zax])   # colmap -> gravity frame
pts = np.array([p.xyz for p in rec.points3D.values()]); trk = np.array([p.track.length() for p in rec.points3D.values()])
from scipy.spatial import cKDTree
cc = np.array([-R.T @ t for R, t, c in cams.values()]); dn = cKDTree(cc).query(pts)[0]
near = dn <= np.percentile(dn, 85); trk = np.where(near, trk, 0)      # drop far (outside-window) points, scale-free
P = (Rg @ pts[trk >= 4].T).T; z = P[:, 2]; keep = (z > np.percentile(z, 20)) & (z < np.percentile(z, 80)); Pw = P[keep]
rng = np.random.default_rng(0); Pw = Pw[rng.choice(len(Pw), min(15000, len(Pw)), replace=False)]
print("fragment imgs %d  points used %d  roll-axis residual %.2f deg" % (len(cams), len(Pw), np.degrees(np.abs(rights @ u)).mean()), flush=True)
best = []; t0 = time.time()
scales = np.exp(np.linspace(np.log(0.4), np.log(8), 62))
for s in scales:
    for th in np.radians(np.arange(0, 360, 2)):
        c, sn = np.cos(th), np.sin(th); X = s * Pw[:, :2] @ np.array([[c, sn], [-sn, c]])
        mn = X.min(0); ij = ((X - mn) / RES).astype(int); h, w = ij[:, 1].max() + 1, ij[:, 0].max() + 1
        if h >= Hg or w >= Wg: continue
        H = np.zeros((h, w), np.float32); np.add.at(H, (ij[:, 1], ij[:, 0]), 1); H = np.minimum(H, 1)
        cost = fftconvolve(Fimg, H[::-1, ::-1], mode='valid') / len(Pw)
        k = np.unravel_index(np.argmin(cost), cost.shape)
        best.append((cost[k], s, th, lo[0] + k[1] * RES - mn[0], lo[1] + k[0] * RES - mn[1]))
best.sort(key=lambda b: b[0]); print("search %.0fs; top5 (cost,s,yaw,tx,ty):" % (time.time() - t0)); 
for b in best[:5]: print("  %.3f s=%.3f yaw=%.0f tx=%.2f ty=%.2f" % (b[0], b[1], np.degrees(b[2]), b[3], b[4]))
# refine top candidates in 3D (7 params in gravity frame: s, yaw, tx, ty, tz)
def cand_tf(x):
    s, th, tx, ty, tz = x; c, sn = np.cos(th), np.sin(th); Rz = np.array([[c, -sn, 0], [sn, c, 0], [0, 0, 1.]])
    return s, Rz @ Rg, np.array([tx, ty, tz])
Pall = pts[trk >= 4]; Pall = Pall[rng.choice(len(Pall), min(15000, len(Pall)), replace=False)]
def resid(x):
    s, R, t = cand_tf(x); X = s * (R @ Pall.T).T + t; _, dist, _ = trimesh.proximity.closest_point(mesh, X); return np.minimum(dist, 1.0)
res = []
seen = []
for b in best[:40]:
    key = (round(b[1], 1), round(np.degrees(b[2]) / 10), round(b[3]), round(b[4]))
    if any(abs(np.log(b[1] / q[1])) < .1 and abs(np.hypot(b[3] - q[3], b[4] - q[4])) < 1.5 and abs((np.degrees(b[2] - q[2]) + 180) % 360 - 180) < 8 for q in seen): continue
    seen.append(b); 
    zc = np.median(((s_ := b[1]) * (Rg @ pts[trk >= 4].T).T)[:, 2]); tz = 1.5 - zc * 0  # z init: put median camera height at 1.5 m
    cz = np.median([(Rg @ (-R.T @ t))[2] * b[1] for R, t, c in cams.values()]); x0 = [b[1], b[2], b[3], b[4], 1.5 - cz]
    sol = least_squares(resid, x0, loss='soft_l1', f_scale=0.1, max_nfev=25, x_scale=[.03, .02, .1, .1, .1])
    r = resid(sol.x); res.append((np.median(r), r.mean(), sol.x, b[0]))
    if len(seen) >= 6: break
res.sort(key=lambda r: r[1]); 
for r in res: print("refined cand: median %.3f mean(trunc) %.3f  s=%.3f yaw=%.1f t=%s  (2D cost %.3f)" % (r[0], r[1], r[2][0], np.degrees(r[2][1]), np.round(r[2][2:], 2), r[3]))
x = res[0][2]; s, R, t = cand_tf(x)
json.dump({'s': s, 'R': R.tolist(), 't': t.tolist(), 'median': res[0][0], 'mean': res[0][1], 'second_mean': res[1][1] if len(res) > 1 else None}, open(sys.argv[3], 'w'))
