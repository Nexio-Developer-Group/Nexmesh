"""Pose refinement: piecewise rigid point-to-plane ICP of SfM points against the Blender model.
usage: 07_refine.py sparse_dir sim3.json out_poses.json"""
import sys, json, re, numpy as np, pycolmap
from scipy.optimize import least_squares
from scipy.spatial.transform import Rotation as Rot, Slerp
sys.path.insert(0, __import__('os').path.dirname(__file__)); from common import *
rec = pycolmap.Reconstruction(sys.argv[1]); S = json.load(open(sys.argv[2])); s, R, t = S['s'], np.array(S['R']), np.array(S['t'])
mesh, d = load_model('work/model.npz'); cams = recon_cams(rec)
fidx = lambda n: int(re.findall(r'\d+', n)[0]) * 1000 + (int(re.findall(r'\d+', n)[1]) if len(re.findall(r'\d+', n)) > 1 else 0)
names = sorted(cams, key=fidx)
pose0 = {n: sim3_apply_pose(s, R, t, *cams[n][:2]) for n in names}            # (Rc2w, C) blender
pid = {i: p for i, p in rec.points3D.items() if p.track.length() >= 3}
obs = {im.name: [p.point3D_id for p in im.points2D if p.has_point3D() and p.point3D_id in pid] for im in rec.images.values()}
SEG = 30; groups = [names[i:i + SEG] for i in range(0, len(names), SEG)]
rng = np.random.default_rng(0); corr = []   # per segment: (centre, rotvec, trans)
def seg_fit(g, prev):
    ids = np.unique(np.concatenate([obs[n] for n in g])); ids = rng.choice(ids, min(15000, len(ids)), replace=False)
    X0 = s * (R @ np.array([pid[i].xyz for i in ids]).T).T + t; c = np.mean([pose0[n][1] for n in g], 0)
    x = np.zeros(6) if prev is None else prev.copy(); w_prior = 40.
    for it in range(6):
        Xc = Rot.from_rotvec(x[:3]).apply(X0 - c) + c + x[3:]
        q, dist, fid = trimesh.proximity.closest_point(mesh, Xc); nrm = mesh.face_normals[fid]
        ok = dist < 0.6
        base = np.zeros(6) if prev is None else prev
        def res2(y):
            Xy = Rot.from_rotvec(y[:3]).apply(X0[ok] - c) + c + y[3:]
            r = np.einsum('ij,ij->i', Xy - q[ok], nrm[ok]) * 100 / np.sqrt(max(ok.sum(), 1)) * 8      # cm, normalised by sqrt(N)
            prior = np.concatenate([y[:3] / np.radians(1.5), y[3:] / 0.08])                          # absolute: 1.5 deg / 8 cm
            smooth = np.concatenate([(y[:3] - base[:3]) / np.radians(2.0), (y[3:] - base[3:]) / 0.10])
            return np.concatenate([r, prior, smooth])
        sol = least_squares(res2, x, loss='soft_l1', f_scale=5.0, max_nfev=15); x = sol.x
    dist_after_x = x
    dist_after = trimesh.proximity.closest_point(mesh, Rot.from_rotvec(x[:3]).apply(X0 - c) + c + x[3:])[1]
    dist_before = trimesh.proximity.closest_point(mesh, X0)[1]
    if ok.mean() < 0.7: x = prev.copy() if prev is not None else np.zeros(6)   # unconstrained segment: carry correction
    return c, x, np.median(dist_before) * 100, np.median(dist_after) * 100, ok.mean()
prev = None
for g in groups:
    c, x, b, a, okf = seg_fit(g, prev); prev = x; corr.append((c, x))
    print(f"{g[0]}..{g[-1]}: median pt->plane {b:5.1f} -> {a:5.1f} cm; corr rot {np.degrees(np.linalg.norm(x[:3])):.2f} deg trans {np.linalg.norm(x[3:]):.2f} m; wall-inlier frac {okf:.0%}", flush=True)
# interpolate corrections per image between segment centres (by index order)
cen_idx = np.array([np.mean([names.index(n) for n in g]) for g in groups])
rots = Rot.from_rotvec(np.array([x[:3] for c, x in corr])); trs = np.array([x[3:] for c, x in corr]); cen = np.array([c for c, x in corr])
sl = Slerp(cen_idx, rots)
out = {}
for k, n in enumerate(names):
    kk = np.clip(k, cen_idx[0], cen_idx[-1]); r = sl(kk); tr = np.array([np.interp(kk, cen_idx, trs[:, j]) for j in range(3)]); c = np.array([np.interp(kk, cen_idx, cen[:, j]) for j in range(3)])
    Rc2w, C = pose0[n]; C2 = r.apply(C - c) + c + tr; R2 = r.as_matrix() @ Rc2w
    cam = cams[n][2]; out[n] = {'R_c2w': R2.tolist(), 'C': C2.tolist(), 'R_c2w_unrefined': Rc2w.tolist(), 'C_unrefined': C.tolist(), 'params': list(cam.params), 'W': cam.width, 'H': cam.height}
json.dump(out, open(sys.argv[3], 'w')); print("saved", len(out), "poses")
