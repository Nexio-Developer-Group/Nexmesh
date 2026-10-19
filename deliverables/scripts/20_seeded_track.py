"""Re-track frames >= START as separate SfM pieces (split where the SfM trajectory teleports), each placed by its own Sim3
fitted to model-anchored PnP localisations; local rigid drift correction with the piece scale fixed.
usage: 20_seeded_track.py poses_sfm.json model.npz frames_dir out.json START seeds.json"""
import sys, os, re, json, time, numpy as np, cv2, torch
from scipy.spatial.transform import Rotation as Rot
sys.path.insert(0, os.path.dirname(__file__)); sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'third_party', 'LightGlue'))
from common import *
from lightglue import LightGlue, SuperPoint
from lightglue.utils import rbd
P = json.load(open(sys.argv[1])); mesh, d = load_model(sys.argv[2]); fdir = sys.argv[3]; outf = sys.argv[4]; START = int(sys.argv[5])
torch.set_num_threads(4); ext = SuperPoint(max_num_keypoints=1536, detection_threshold=0.003).eval(); mat = LightGlue(features='superpoint', depth_confidence=0.9, width_confidence=0.95).eval()
ren = Renderer(mesh, d['OBJ']); q = next(iter(P.values()))['params']; K = np.array([[q[0], 0, q[2]], [0, q[1], q[3]], [0, 0, 1.]]); dist = np.array(q[4:8])
idx = lambda n: int(re.findall(r'\d+', n)[0]) + (int(re.findall(r'\d+', n)[1]) / 1000. if len(re.findall(r'\d+', n)) > 1 else 0)
plain = sorted([n for n in P if n.count('_') == 1], key=idx); sfm = {n: (np.array(P[n]['R_c2w']), np.array(P[n]['C'])) for n in P}
cur = {n: sfm[n] for n in plain if idx(n) < START}                       # trusted poses (verified part)
log = lambda *a: print(*a, flush=True)
# ---- split pieces where the SfM trajectory teleports ----
tgt = [n for n in plain if idx(n) >= START]; piece = {}; pid = 0; prev = None
for n in tgt:
    if prev is not None:
        dt = (idx(n) - idx(prev)) / 3.; v = np.linalg.norm(sfm[n][1] - sfm[prev][1]) / dt; w = np.degrees(Rot.from_matrix(sfm[n][0] @ sfm[prev][0].T).magnitude()) / dt
        if v > 3.0 or w > 250: pid += 1; log('piece break before', n, 'speed %.1f m/s, %.0f deg/s' % (v, w))
    piece[n] = pid; prev = n
log('pieces:', {k: sum(1 for p in piece.values() if p == k) for k in set(piece.values())})
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
def umeyama(A, B, fix_scale=None):
    ma, mb = A.mean(0), B.mean(0); a, b = A - ma, B - mb; U, D, Vt = np.linalg.svd(b.T @ a / len(A)); Sg = np.eye(3); Sg[2, 2] = np.sign(np.linalg.det(U @ Vt)); R = U @ Sg @ Vt
    s = fix_scale if fix_scale else (D * np.diag(Sg)).sum() / ((a ** 2).sum() / len(A)); return s, R, mb - s * R @ ma
def robust_fit(A, B, fix_scale=None, iters=4):
    keep = np.ones(len(A), bool)
    for _ in range(iters):
        s, R, t = umeyama(A[keep], B[keep], fix_scale); r = np.linalg.norm(s * (R @ A.T).T + t - B, axis=1); keep = r < max(2.5 * np.median(r), 0.05)
        if keep.sum() < 3: break
    return s, R, t, keep
anch = {}          # name -> (Rc2w, C) PnP pose;  
def level(Rn): f = Rn[:, 2]; rt = np.cross(f, [0, 0, 1.]); rt /= np.linalg.norm(rt); return np.stack([rt, np.cross(f, rt), f], 1)
def localise(n, refs, guess=None):
    fb = get(n); P3, P2 = [], []
    for r in refs:
        Rr, Cr = cur[r]; fa = get(r)
        with torch.no_grad(): m = rbd(mat({'image0': fa, 'image1': fb}))['matches'].cpu().numpy()
        if len(m) < 12: continue
        X = pts3d(Rr, Cr, fa['keypoints'][0].cpu().numpy()[m[:, 0]]); ok = np.isfinite(X).all(1); P3.append(X[ok]); P2.append(fb['keypoints'][0].cpu().numpy()[m[ok, 1]])
    if not P3 or sum(len(x) for x in P3) < 25: return None, 'few matches'
    P3 = np.concatenate(P3); P2 = np.concatenate(P2).astype(np.float64)
    okp, rv, tv, inl = cv2.solvePnPRansac(P3, P2, K, dist, iterationsCount=1500, reprojectionError=4.0, confidence=0.9995, flags=cv2.SOLVEPNP_EPNP)
    if not okp or inl is None or len(inl) < 40: return None, 'pnp inliers %d' % (0 if inl is None else len(inl))
    inl = inl[:, 0]; _, rv, tv = cv2.solvePnP(P3[inl], P2[inl], K, dist, rv, tv, True, cv2.SOLVEPNP_ITERATIVE); Rcw, _ = cv2.Rodrigues(rv); Rn, Cn = level(Rcw.T), (-Rcw.T @ tv).ravel()
    if guess is not None:
        dp = np.linalg.norm(Cn - guess[1]); da = np.degrees(Rot.from_matrix(Rn @ guess[0].T).magnitude())
        if dp > 0.8 or da > 12: return None, 'reject vs guess %.2f m %.1f deg' % (dp, da)
    return (Rn, Cn, len(inl)), 'ok'
def rot_mean(Rs):
    M = sum(Rs); U, _, Vt = np.linalg.svd(M); return U @ np.diag([1, 1, np.sign(np.linalg.det(U @ Vt))]) @ Vt
def window_fit(n, pc, k=10, fixs=None):
    an = sorted([a for a in anch if piece[a] == pc], key=lambda a: abs(idx(a) - idx(n)))[:k]
    if len(an) < 2: return None
    A = np.array([sfm[a][1] for a in an]); B = np.array([anch[a][1] for a in an]); spread = np.linalg.norm(A.std(0))
    R = rot_mean([anch[a][0] @ sfm[a][0].T for a in an])                              # orientation offsets: well-defined even if the anchors are close together
    if fixs: s = fixs
    elif spread > 0.8 and len(an) >= 4: s = robust_fit(A, B)[0]
    else: s = 1.0
    t = B.mean(0) - s * R @ A.mean(0)
    if spread > 0.5 and len(an) >= 4:                                                  # enough spread: full rigid fit with fixed scale refines R,t
        _, R2, t2, keep = robust_fit(A, B, s)
        if np.degrees(Rot.from_matrix(R2 @ R.T).magnitude()) < 8: R, t = R2, t2
    return s, R, t
def apply_sim(n, s, R, t): Rn, Cn = sfm[n]; return R @ Rn, s * R @ Cn + t
seeds = json.load(open(sys.argv[6])) if len(sys.argv) > 6 else {}
def pose_from(x, y, z, yaw, pitch):
    y_, p_ = np.radians(yaw), np.radians(pitch); f = np.array([np.cos(p_) * np.cos(y_), np.cos(p_) * np.sin(y_), np.sin(p_)]); rt = np.cross(f, [0, 0, 1.]); rt /= np.linalg.norm(rt); return np.stack([rt, np.cross(f, rt), f], 1), np.array([x, y, z])
prov = {}; piece_scale = {}; t0 = time.time(); stats = {'ok': 0, 'fail': 0, 'seed': 0}; done = set()
for n, v in seeds.items():
    R, C = pose_from(*v); anch[n] = (R, C, 999); cur[n] = (R, C); done.add(n); stats['seed'] += 1
trusted_near = [x for x in cur if idx(x) < START and idx(x) > START - 40]
todo = set(tgt) - done
while todo:
    proc = [idx(x) for x in list(done) + trusted_near]
    n = min(todo, key=lambda t: (min(abs(idx(t) - p) for p in proc), idx(t)))        # frame closest in time to something already placed
    todo.discard(n); done.add(n); pc = piece[n]
    w = window_fit(n, pc, 10, piece_scale.get(pc)); guess = apply_sim(n, *w) if w is not None else None
    pool = list(anch) + list(prov) + trusted_near
    refs = sorted(pool, key=lambda a: abs(idx(a) - idx(n)) + (0 if (a in anch or a not in prov) else 1.5))[:3]
    for a in refs:
        if a not in cur: cur[a] = anch[a][:2] if a in anch else prov[a]
    res, why = localise(n, refs, guess)
    if res is None and guess is not None and why.startswith('reject'): res, why = None, why
    if res is None:
        stats['fail'] += 1; log(n, 'piece', pc, 'no anchor:', why)
        if guess is not None: prov[n] = (level(guess[0]), guess[1])
        else:
            r0 = refs[0]; Rr, Cr = cur[r0]; prov[n] = (Rr, Cr)                                       # continuity: copy nearest placed neighbour
        cur[n] = prov[n]; continue
    anch[n] = res; cur[n] = (res[0], res[1]); stats['ok'] += 1
    A = [a for a in anch if piece[a] == pc]
    if len(A) >= 8 and (pc not in piece_scale or len(A) % 12 == 0):
        s_, R_, t_, keep = robust_fit(np.array([sfm[a][1] for a in A]), np.array([anch[a][1] for a in A])); piece_scale[pc] = float(s_)
    log(f"{n} piece {pc} anchor inl {res[2]} guess-diff {'-' if guess is None else '%.2f m' % np.linalg.norm(res[1]-guess[1])} scale {piece_scale.get(pc, float('nan')):.3f} [{time.time()-t0:.0f}s]")
    json.dump({a: [anch[a][0].tolist(), anch[a][1].tolist(), anch[a][2], piece[a]] for a in anch}, open(outf + '.anchors.json', 'w'))
# ---- final: per piece fixed scale, local rigid window over nearest anchors ----
final = {}
for n in P:
    p = P[n]; Rn, Cn = sfm[n]
    if n in piece and idx(n) >= START:
        pc = piece[n]; A = [a for a in anch if piece[a] == pc]
        if len(A) >= 3:
            s_fix = piece_scale.get(pc) or robust_fit(np.array([sfm[a][1] for a in A]), np.array([anch[a][1] for a in A]))[0]
            s, R, t = window_fit(n, pc, 8, s_fix)
            Rn, Cn = apply_sim(n, s, R, t); Rn = level(Rn)
        else: continue          # piece could not be placed: leave unposed
    elif n.count('_') > 1 and idx(n) >= START: continue
    final[n] = dict(p, R_c2w=Rn.tolist(), C=Cn.tolist(), R_c2w_unrefined=Rn.tolist(), C_unrefined=Cn.tolist(), anchor=(n in anch))
json.dump(final, open(outf, 'w')); log('anchors', stats, 'frames out', len(final), 'piece scales', piece_scale)
