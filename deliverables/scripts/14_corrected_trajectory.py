"""Drift-corrected trajectory: SfM poses (trusted up to START) + model-anchored PnP anchors afterwards; correction interpolated between anchors.
usage: 14_corrected_trajectory.py poses_sfm.json model.npz frames_dir out.json START_IDX [STRIDE]"""
import sys, os, re, json, time, numpy as np, cv2, torch
from scipy.spatial.transform import Rotation as Rot, Slerp
sys.path.insert(0, os.path.dirname(__file__)); sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'third_party', 'LightGlue'))
from common import *
from lightglue import LightGlue, SuperPoint
from lightglue.utils import rbd
P = json.load(open(sys.argv[1])); mesh, d = load_model(sys.argv[2]); fdir = sys.argv[3]; outf = sys.argv[4]; START = int(sys.argv[5]); STRIDE = int(sys.argv[6]) if len(sys.argv) > 6 else 2
torch.set_num_threads(4); ext = SuperPoint(max_num_keypoints=1536, detection_threshold=0.003).eval(); mat = LightGlue(features='superpoint', depth_confidence=0.9, width_confidence=0.95).eval()
ren = Renderer(mesh, d['OBJ']); q = next(iter(P.values()))['params']; K = np.array([[q[0], 0, q[2]], [0, q[1], q[3]], [0, 0, 1.]]); dist = np.array(q[4:8])
idx = lambda n: int(re.findall(r'\d+', n)[0]) + (int(re.findall(r'\d+', n)[1]) / 1000. if len(re.findall(r'\d+', n)) > 1 else 0)
names = sorted(P, key=idx); sfm = {n: (np.array(P[n]['R_c2w']), np.array(P[n]['C'])) for n in names}
cur = {n: sfm[n] for n in names if idx(n) < START}            # current (corrected) poses; trusted part = SfM
def feats(n):
    g = cv2.imread(os.path.join(fdir, n), cv2.IMREAD_GRAYSCALE); g = cv2.createCLAHE(3.0, (8, 8)).apply(cv2.resize(g, (1280, 720), interpolation=cv2.INTER_AREA))
    with torch.no_grad(): f = ext.extract(torch.from_numpy(g)[None, None].float().div(255))
    f['keypoints'] = f['keypoints'] * 1.5; f['image_size'] = torch.tensor([[1920., 1080.]]); return f
cache = {}
def get(n):
    if n not in cache: cache[n] = feats(n)
    return cache[n]
def pts3d(Rc2w, C, kp):
    und = cv2.undistortPoints(kp.reshape(-1, 1, 2).astype(np.float64), K, dist).reshape(-1, 2); dirs = np.c_[und, np.ones(len(und))] @ Rc2w.T; dirs /= np.linalg.norm(dirs, axis=1, keepdims=True)
    tri = ren.ray.intersects_first(np.tile(C, (len(dirs), 1)), dirs); hit = tri >= 0; X = np.full((len(dirs), 3), np.nan)
    t = np.einsum('ij,ij->i', ren.p0[tri[hit]] - C, ren.n[tri[hit]]) / np.einsum('ij,ij->i', dirs[hit], ren.n[tri[hit]]); X[hit] = C + dirs[hit] * t[:, None]; return X
trusted = [n for n in names if idx(n) < START and n.count('_') == 1]
anchors = [n for n in trusted[-40::8]]                       # a few trusted references to start the chain
anch_corr = {}                                              # name -> (R_corr, t_corr) : X_corrected = R_corr X_sfm + t_corr
targets = [n for n in names if idx(n) >= START and n.count('_') == 1][::STRIDE]
log = lambda *a: print(*a, flush=True); t0 = time.time(); R_c, t_c = np.eye(3), np.zeros(3); ok_n = rej = fails = 0
def apply(n, Rc, tc): R, C = sfm[n]; return Rc @ R, Rc @ C + tc
for n in targets:
    refs = sorted(anchors, key=lambda r: abs(idx(r) - idx(n)))[:3]
    Rg, Cg = apply(n, R_c, t_c); fb = get(n); P3, P2 = [], []
    for r in refs:
        Rr, Cr = cur[r]; fa = get(r)
        with torch.no_grad(): m = rbd(mat({'image0': fa, 'image1': fb}))['matches'].cpu().numpy()
        if len(m) < 12: continue
        X = pts3d(Rr, Cr, fa['keypoints'][0].cpu().numpy()[m[:, 0]]); ok = np.isfinite(X).all(1); P3.append(X[ok]); P2.append(fb['keypoints'][0].cpu().numpy()[m[ok, 1]])
    if not P3 or sum(len(x) for x in P3) < 25: fails += 1; log(n, 'no anchor (few matches)'); cur[n] = (Rg, Cg); anchors.append(n); continue
    P3 = np.concatenate(P3); P2 = np.concatenate(P2).astype(np.float64)
    rv0, _ = cv2.Rodrigues(Rg.T); tv0 = (-Rg.T @ Cg).reshape(3, 1)                     # guess as initial pose
    okp, rv, tv, inl = cv2.solvePnPRansac(P3, P2, K, dist, rvec=rv0.copy(), tvec=tv0.copy(), useExtrinsicGuess=True, iterationsCount=800, reprojectionError=4.0, confidence=0.999, flags=cv2.SOLVEPNP_ITERATIVE)
    if not okp or inl is None or len(inl) < 40: fails += 1; log(n, 'PnP fail', 0 if inl is None else len(inl)); cur[n] = (Rg, Cg); anchors.append(n); continue
    inl = inl[:, 0]; _, rv, tv = cv2.solvePnP(P3[inl], P2[inl], K, dist, rv, tv, True, cv2.SOLVEPNP_ITERATIVE); Rcw, _ = cv2.Rodrigues(rv); Rn, Cn = Rcw.T, (-Rcw.T @ tv).ravel()
    f = Rn[:, 2]; rt = np.cross(f, [0, 0, 1.]); rt /= np.linalg.norm(rt); Rn = np.stack([rt, np.cross(f, rt), f], 1)      # gimbal-stabilised: level the camera (zero roll)
    dpos = np.linalg.norm(Cn - Cg); dang = np.degrees(Rot.from_matrix(Rn @ Rg.T).magnitude())
    if dpos > 1.0 or dang > 10: rej += 1; log(n, 'REJECT jump %.2f m %.1f deg (inl %d)' % (dpos, dang, len(inl))); cur[n] = (Rg, Cg); anchors.append(n); continue
    R_c = Rn @ sfm[n][0].T; t_c = Cn - R_c @ sfm[n][1]; anch_corr[n] = (R_c.copy(), t_c.copy()); cur[n] = (Rn, Cn); anchors.append(n); ok_n += 1
    log(f"{n} anchor inl {len(inl)}/{len(P3)} corr shift {dpos:.2f} m {dang:.1f} deg  [{time.time()-t0:.0f}s]")
    json.dump({k: [v[0].tolist(), v[1].tolist()] for k, v in anch_corr.items()}, open(outf + '.anchors.json', 'w'))
# interpolate correction for every registered frame >= START
an = sorted(anch_corr, key=idx); ai = np.array([idx(a) for a in an])
final = {}
for n in names:
    if idx(n) < START or len(an) == 0: Rn, Cn = sfm[n]
    else:
        x = idx(n); j = np.searchsorted(ai, x)
        if j == 0: Rc, tc = anch_corr[an[0]]
        elif j >= len(an): Rc, tc = anch_corr[an[-1]]
        else:
            a, b = an[j - 1], an[j]; w = (x - ai[j - 1]) / (ai[j] - ai[j - 1]); Ra, ta = anch_corr[a]; Rb, tb = anch_corr[b]
            Rc = Slerp([0, 1], Rot.from_matrix([Ra, Rb]))(w).as_matrix(); tc = (1 - w) * ta + w * tb
        Rn, Cn = apply(n, Rc, tc)
    p = P[n]; final[n] = dict(p, R_c2w=Rn.tolist(), C=Cn.tolist(), R_c2w_unrefined=Rn.tolist(), C_unrefined=Cn.tolist(), anchor=(n in anch_corr))
json.dump(final, open(outf, 'w')); log('anchors', ok_n, 'rejected', rej, 'failed', fails, 'frames', len(final))
