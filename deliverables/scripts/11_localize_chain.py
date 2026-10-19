"""Model-based chain localisation. Reference frames get 3D keypoints by ray-casting the Blender model; new frames are matched
(SuperPoint+LightGlue) to nearby references and solved with PnP+RANSAC. Localised frames become references.
usage: 11_localize_chain.py poses_in.json model.npz frames_dir out.json start_name end_index [step]
frames processed in time order: all frame files whose index is within (start..end), not already in poses_in."""
import sys, os, re, json, time, numpy as np, cv2, torch
sys.path.insert(0, os.path.dirname(__file__)); sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'third_party', 'LightGlue'))
from common import *
from lightglue import LightGlue, SuperPoint
from lightglue.utils import rbd
poses = json.load(open(sys.argv[1])); mesh, d = load_model(sys.argv[2]); fdir = sys.argv[3]; outf = sys.argv[4]; i0 = int(sys.argv[5]); i1 = int(sys.argv[6])
dev = 'cpu'; torch.set_num_threads(4)
ext = SuperPoint(max_num_keypoints=1536, detection_threshold=0.003).eval().to(dev); mat = LightGlue(features='superpoint', depth_confidence=0.9, width_confidence=0.95).eval().to(dev)
ren = Renderer(mesh, d['OBJ']); q = next(iter(poses.values()))['params']; W, H = 1920, 1080
K = np.array([[q[0], 0, q[2]], [0, q[1], q[3]], [0, 0, 1.]]); dist = np.array(q[4:8])
def idx_of(n): m = re.findall(r'\d+', n); return int(m[0]) + (int(m[1]) / 1000. if len(m) > 1 else 0)
def feats(n):
    g = cv2.imread(os.path.join(fdir, n), cv2.IMREAD_GRAYSCALE); g = cv2.resize(g, (1280, 720), interpolation=cv2.INTER_AREA); g = cv2.createCLAHE(3.0, (8, 8)).apply(g)   # dark/flat frames
    t = torch.from_numpy(g)[None, None].float().div(255).to(dev)
    with torch.no_grad(): f = ext.extract(t)
    f['keypoints'] = f['keypoints'] * 1.5; f['image_size'] = torch.tensor([[1920., 1080.]])      # kps back to 1920 px coords
    return f
cache = {}
def get(n):
    if n not in cache: cache[n] = feats(n)
    return cache[n]
def pts3d(n, f):
    kp = f['keypoints'][0].cpu().numpy().astype(np.float64); und = cv2.undistortPoints(kp.reshape(-1, 1, 2), K, dist).reshape(-1, 2)
    Rc2w = np.array(poses[n]['R_c2w']); C = np.array(poses[n]['C'])
    dirs = np.c_[und, np.ones(len(und))] @ Rc2w.T; dirs /= np.linalg.norm(dirs, axis=1, keepdims=True)
    tri = ren.ray.intersects_first(np.tile(C, (len(dirs), 1)), dirs); hit = tri >= 0
    X = np.full((len(dirs), 3), np.nan)
    t = np.einsum('ij,ij->i', ren.p0[tri[hit]] - C, ren.n[tri[hit]]) / np.einsum('ij,ij->i', dirs[hit], ren.n[tri[hit]])
    X[hit] = C + dirs[hit] * t[:, None]; return X
DENSE = os.environ.get('DENSE', '0') == '1'
cand = sorted([f for f in os.listdir(fdir) if f.endswith('.jpg') and f not in poses and i0 <= idx_of(f) <= i1 and (DENSE or f.count('_') == 1)], key=idx_of)
cand = cand[::int(os.environ.get('STRIDE', '1'))]
log = lambda *a: print(*a, flush=True); t0 = time.time(); ok_n = 0; fails = 0
for n in cand:
    refs = sorted(poses, key=lambda r: abs(idx_of(r) - idx_of(n)))[:3]
    P3, P2, src = [], [], []
    fb = get(n)
    for r in refs:
        fa = get(r)
        with torch.no_grad(): m = rbd(mat({'image0': fa, 'image1': fb}))
        mi = m['matches'].cpu().numpy()
        if len(mi) < 12: continue
        X = pts3d(r, {'keypoints': fa['keypoints']})[mi[:, 0]]; ok = np.isfinite(X).all(1)
        P3.append(X[ok]); P2.append(fb['keypoints'][0].cpu().numpy()[mi[ok, 1] if False else mi[:, 1]][ok]); src += [r] * ok.sum()
    if not P3 or sum(len(x) for x in P3) < 20: fails += 1; log(n, 'FAIL few matches'); continue
    P3 = np.concatenate(P3); P2 = np.concatenate(P2).astype(np.float64)
    okp, rv, tv, inl = cv2.solvePnPRansac(P3, P2, K, dist, iterationsCount=500, reprojectionError=4.0, confidence=0.999, flags=cv2.SOLVEPNP_EPNP)
    if not okp or inl is None or len(inl) < 20: fails += 1; log(n, 'FAIL pnp inliers', 0 if inl is None else len(inl)); continue
    inl = inl[:, 0]; okp, rv, tv = cv2.solvePnP(P3[inl], P2[inl], K, dist, rv, tv, True, cv2.SOLVEPNP_ITERATIVE)
    R_cw, _ = cv2.Rodrigues(rv); Rc2w = R_cw.T; C = (-R_cw.T @ tv).ravel()
    # sanity: reject jumps (> 1.5 m from nearest ref in time, assuming 3fps and drone speed < ~4 m/s)
    ref0 = poses[refs[0]]; jump = np.linalg.norm(C - np.array(ref0['C'])); span = abs(idx_of(n) - idx_of(refs[0])) / 3. + 0.1
    if jump > 4.0 * span + 0.3: fails += 1; log(n, 'FAIL jump %.2f m over %.1fs' % (jump, span)); continue
    poses[n] = {'R_c2w': Rc2w.tolist(), 'C': C.tolist(), 'R_c2w_unrefined': Rc2w.tolist(), 'C_unrefined': C.tolist(), 'params': list(q), 'W': W, 'H': H, 'localised_by': 'chain_pnp', 'inliers': int(len(inl))}
    ok_n += 1; log(f"{n} OK inliers {len(inl)}/{len(P3)} jump {jump:.2f} m  [{time.time()-t0:.0f}s]")
    json.dump(poses, open(outf, 'w'))
json.dump(poses, open(outf, 'w')); log('localised', ok_n, 'failed', fails)
