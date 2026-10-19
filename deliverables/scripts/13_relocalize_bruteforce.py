"""Relocalise target frames against ALL registered reference frames (no temporal adjacency needed).
usage: 13_relocalize_bruteforce.py poses_in.json model.npz frames_dir out.json ref_stride target1,target2,...
Per target: SuperPoint+LightGlue vs every ref_stride-th registered frame; 2D-3D from model ray-casting at each ref; PnP RANSAC."""
import sys, os, re, json, time, numpy as np, cv2, torch
sys.path.insert(0, os.path.dirname(__file__)); sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'third_party', 'LightGlue'))
from common import *
from lightglue import LightGlue, SuperPoint
from lightglue.utils import rbd
poses = json.load(open(sys.argv[1])); mesh, d = load_model(sys.argv[2]); fdir = sys.argv[3]; outf = sys.argv[4]; stride = int(sys.argv[5]); targets = sys.argv[6].split(',')
torch.set_num_threads(4); ext = SuperPoint(max_num_keypoints=1536, detection_threshold=0.003).eval(); mat = LightGlue(features='superpoint', depth_confidence=0.9, width_confidence=0.95).eval()
ren = Renderer(mesh, d['OBJ']); q = next(iter(poses.values()))['params']; W, H = 1920, 1080
K = np.array([[q[0], 0, q[2]], [0, q[1], q[3]], [0, 0, 1.]]); dist = np.array(q[4:8])
idx_of = lambda n: int(re.findall(r'\d+', n)[0])
def feats(n):
    g = cv2.imread(os.path.join(fdir, n), cv2.IMREAD_GRAYSCALE); g = cv2.createCLAHE(3.0, (8, 8)).apply(cv2.resize(g, (1280, 720), interpolation=cv2.INTER_AREA))
    with torch.no_grad(): f = ext.extract(torch.from_numpy(g)[None, None].float().div(255))
    f['keypoints'] = f['keypoints'] * 1.5; f['image_size'] = torch.tensor([[1920., 1080.]]); return f
def pts3d(n, kp):
    und = cv2.undistortPoints(kp.reshape(-1, 1, 2).astype(np.float64), K, dist).reshape(-1, 2); Rc2w = np.array(poses[n]['R_c2w']); C = np.array(poses[n]['C'])
    dirs = np.c_[und, np.ones(len(und))] @ Rc2w.T; dirs /= np.linalg.norm(dirs, axis=1, keepdims=True)
    tri = ren.ray.intersects_first(np.tile(C, (len(dirs), 1)), dirs); hit = tri >= 0; X = np.full((len(dirs), 3), np.nan)
    t = np.einsum('ij,ij->i', ren.p0[tri[hit]] - C, ren.n[tri[hit]]) / np.einsum('ij,ij->i', dirs[hit], ren.n[tri[hit]]); X[hit] = C + dirs[hit] * t[:, None]; return X
refs = sorted(poses, key=idx_of)[::stride]; t0 = time.time(); RF = {}
for r in refs: RF[r] = feats(r)
print('ref features', len(RF), '%.0fs' % (time.time() - t0), flush=True)
for tn in targets:
    fb = feats(tn); P3, P2, per = [], [], []
    for r in refs:
        with torch.no_grad(): m = rbd(mat({'image0': RF[r], 'image1': fb}))['matches'].cpu().numpy()
        if len(m) < 25: continue
        kpa = RF[r]['keypoints'][0].cpu().numpy(); X = pts3d(r, kpa[m[:, 0]]); ok = np.isfinite(X).all(1)
        if ok.sum() < 20: continue
        P3.append(X[ok]); P2.append(fb['keypoints'][0].cpu().numpy()[m[:, 1]][ok]); per.append((r, int(ok.sum())))
    if not P3: print(tn, 'no candidates', flush=True); continue
    P3 = np.concatenate(P3); P2 = np.concatenate(P2).astype(np.float64)
    okp, rv, tv, inl = cv2.solvePnPRansac(P3, P2, K, dist, iterationsCount=3000, reprojectionError=4.0, confidence=0.9999, flags=cv2.SOLVEPNP_EPNP)
    ni = 0 if inl is None else len(inl); top = sorted(per, key=lambda x: -x[1])[:4]
    print(tn, 'candidates', len(per), 'corr', len(P3), 'inliers', ni, 'top refs', top, flush=True)
    if okp and ni >= 40:
        inl = inl[:, 0]; _, rv, tv = cv2.solvePnP(P3[inl], P2[inl], K, dist, rv, tv, True, cv2.SOLVEPNP_ITERATIVE); R_cw, _ = cv2.Rodrigues(rv)
        poses[tn] = {'R_c2w': R_cw.T.tolist(), 'C': (-R_cw.T @ tv).ravel().tolist(), 'R_c2w_unrefined': R_cw.T.tolist(), 'C_unrefined': (-R_cw.T @ tv).ravel().tolist(), 'params': list(q), 'W': W, 'H': H, 'localised_by': 'bruteforce_pnp', 'inliers': int(ni)}
        print('   -> localised at', np.round((-R_cw.T @ tv).ravel(), 2), flush=True); json.dump(poses, open(outf, 'w'))
