"""Sim3 alignment COLMAP -> Blender anchored at first-frame camera. usage: 04_align.py sparse_dir model.npz model.json out.json"""
import sys, json, numpy as np, pycolmap
from scipy.optimize import least_squares
from scipy.spatial.transform import Rotation as Rot
sys.path.insert(0, __import__('os').path.dirname(__file__)); from common import *
rec = pycolmap.Reconstruction(sys.argv[1]); mesh, d = load_model(sys.argv[2]); mj = json.load(open(sys.argv[3]))
cams = recon_cams(rec); ren = Renderer(mesh, d['OBJ'])
M = np.array(mj['cam_matrix_world']); Rb = M[:3, :3] @ FLIP; Cb = M[:3, 3]      # anchor opencv c2w + centre
a_name = 'f_00001.jpg'; R_cw, t_cw, cam = cams[a_name]; K = K_of(cam)
R0 = Rb @ R_cw                                                              # colmap world -> blender world rotation
# scale init from depth ratio at anchor view
pts = np.array([p.xyz for p in rec.points3D.values()]); trk = np.array([p.track.length() for p in rec.points3D.values()])
pc = (R_cw @ pts.T).T + t_cw; vis = (pc[:, 2] > 0.2)
uv = (K @ pc[vis].T).T; uv = uv[:, :2] / uv[:, 2:]; z = pc[vis][:, 2]
ok = (uv[:, 0] > 0) & (uv[:, 0] < 1920) & (uv[:, 1] > 0) & (uv[:, 1] < 1080)
depth, tri = ren.render(K, 1920, 1080, Rb, Cb, scale=0.25)
zi = depth[(uv[ok, 1] * .25).astype(int).clip(0, depth.shape[0] - 1), (uv[ok, 0] * .25).astype(int).clip(0, depth.shape[1] - 1)]
good = np.isfinite(zi); ratios = zi[good] / z[ok][good]
s0 = float(np.median(ratios)); print("anchor depth-ratio scale: median %.4f  IQR [%.3f, %.3f]  n=%d" % (s0, *np.percentile(ratios, [25, 75]), good.sum()))
# refine all 7 DoF (anchor soft prior) on point-to-model distance
sel = np.where(trk >= 4)[0]; rng = np.random.default_rng(0); sel = rng.choice(sel, min(30000, len(sel)), replace=False); P = pts[sel]
cen0 = -R_cw.T @ t_cw
def tf(x):
    s = np.exp(x[0]); R = Rot.from_rotvec(x[1:4]).as_matrix() @ R0; t = Cb - s * R @ cen0 + x[4:7]; return s, R, t
def resid(x, prior_w=30.):
    s, R, t = tf(x); X = s * (R @ P.T).T + t
    _, dist, _ = trimesh.proximity.closest_point(mesh, X)
    r = dist
    return np.concatenate([r, prior_w * np.append(x[1:4], x[4:7])])
x0 = np.array([np.log(s0), 0, 0, 0, 0, 0, 0.])
r0 = resid(x0); print("init: median point->plane %.3f m  mean %.3f" % (np.median(r0[:len(P)]), r0[:len(P)].mean()))
sol = least_squares(resid, x0, loss='soft_l1', f_scale=0.15, max_nfev=40, x_scale=[.05, .02, .02, .02, .1, .1, .1])
s, R, t = tf(sol.x); r1 = resid(sol.x)[:len(P)]
print("refined: scale %.4f (init %.4f) rot-corr %.2f deg, trans-corr %.3f m;  median point->plane %.3f  mean %.3f" % (s, s0, np.degrees(np.linalg.norm(sol.x[1:4])), np.linalg.norm(sol.x[4:7]), np.median(r1), r1.mean()))
json.dump({'s': s, 'R': R.tolist(), 't': t.tolist(), 's0': s0, 'anchor_only_R': R0.tolist()}, open(sys.argv[4], 'w'))
