"""Bake the ground surface (z=Z0) as a top-down texture from the drone frames.
usage: floor_bake.py poses.json occluder_model.npz frames_dir out_dir [Z0=0.0] [ts=0.0125]"""
import sys, os, json, time, numpy as np, cv2
sys.path.insert(0, os.environ['SCRIPTS']); from common import *
poses = json.load(open(sys.argv[1])); mesh, md = load_model(sys.argv[2]); fdir = sys.argv[3]; out = sys.argv[4]; Z0 = float(sys.argv[5]) if len(sys.argv) > 5 else 0.0; ts = float(sys.argv[6]) if len(sys.argv) > 6 else 0.0125
os.makedirs(out, exist_ok=True); t0 = time.time(); log = lambda *a: print("[%5.0fs]" % (time.time() - t0), *a, flush=True)
plate = np.load('plate_final.npy'); RES = 0.05; lo = np.array([-21., -14.])
ys_, xs_ = np.where(plate > 0); xmin, xmax = lo[0] + xs_.min() * RES, lo[0] + (xs_.max() + 1) * RES; ymin, ymax = lo[1] + ys_.min() * RES, lo[1] + (ys_.max() + 1) * RES
W = int(np.ceil((xmax - xmin + 0.2) / ts)); H = int(np.ceil((ymax - ymin + 0.2) / ts)); gx0 = xmin - 0.1; gy1 = ymax + 0.1; log("grid", W, H, "texel %.2f cm" % (ts * 100), "extent %.1f x %.1f m" % (W * ts, H * ts))
jj, ii = np.mgrid[0:H, 0:W]; xw = gx0 + (ii + .5) * ts; yw = gy1 - (jj + .5) * ts
mx = ((xw - lo[0]) / RES).astype(int); my = ((yw - lo[1]) / RES).astype(int); ok = (mx >= 0) & (mx < plate.shape[1]) & (my >= 0) & (my < plate.shape[0]); inplate = np.zeros((H, W), bool); inplate[ok] = plate[my[ok], mx[ok]] > 0
tex = np.flatnonzero(inplate.ravel()); X = np.stack([xw.ravel()[tex], yw.ravel()[tex], np.full(len(tex), Z0)], 1).astype(np.float32); N = len(tex); log("plate texels", N, "area %.0f m2" % (N * ts * ts))
# spatial cells
CELL = 2.0; cx = np.floor((X[:, 0] - gx0) / CELL).astype(int); cy = np.floor((X[:, 1] - (gy1 - H * ts)) / CELL).astype(int); ncy = cy.max() + 1; cid = cx * ncy + cy; order = np.argsort(cid); cs = cid[order]
def cand(C, R=7.0):
    c0x, c1x = int(np.floor((C[0] - R - gx0) / CELL)), int(np.floor((C[0] + R - gx0) / CELL)); c0y, c1y = int(np.floor((C[1] - R - (gy1 - H * ts)) / CELL)), int(np.floor((C[1] + R - (gy1 - H * ts)) / CELL)); parts = []
    for a in range(max(c0x, 0), c1x + 1):
        lo_, hi_ = a * ncy + max(c0y, 0), a * ncy + min(c1y, ncy - 1)
        if hi_ >= lo_: parts.append(order[np.searchsorted(cs, lo_):np.searchsorted(cs, hi_, side='right')])
    return np.concatenate(parts) if parts else np.zeros(0, int)
names = sorted(n for n in poses if n.count('_') == 1); ren = Renderer(mesh, md['OBJ']); DS = 1 / 6.; maps = {}
for n in names:
    p = poses[n]; q = p['params']; K = np.array([[q[0], 0, q[2]], [0, q[1], q[3]], [0, 0, 1.]]); dep, tri = ren.render(K, p['W'], p['H'], np.array(p['R_c2w_unrefined']), np.array(p['C_unrefined']), scale=DS)
    maps[n] = cv2.erode(np.where(np.isfinite(dep), dep, 1e4).astype(np.float32), np.ones((3, 3), np.uint8))
import pycolmap
rec = pycolmap.Reconstruction(os.environ['SFM']); S_ext4 = json.load(open(os.environ['SIM3']))['s']; import re; fidx = lambda n: int(re.findall(r'\d+', n)[0]); pscale = lambda n: 1.0 if fidx(n) < 1545 else 0.929 if fidx(n) < 1642 else 0.695 if fidx(n) < 1832 else 0.871
byname = {im.name: im for im in rec.images.values()}; occ = {}; DSo = 1 / 6.
for n in names:
    im = byname.get(n)
    if im is None: continue
    p = poses[n]; q = p['params']; Kc = np.array([[q[0], 0, q[2]], [0, q[1], q[3]], [0, 0, 1.]]); T = im.cam_from_world().matrix(); Rcw, tcw = T[:, :3], T[:, 3]
    pts2 = []; pts3 = []
    for pt in im.points2D:
        if pt.has_point3D(): pts2.append(pt.xy); pts3.append(rec.points3D[pt.point3D_id].xyz)
    if len(pts2) < 20: continue
    pts2 = np.array(pts2, np.float64); pts3 = np.array(pts3); pc = (Rcw @ pts3.T).T + tcw; sc = S_ext4 * (pscale(n) if fidx(n) >= 1541 else 1.0); pm = pc * sc
    Rf = np.array(p['R_c2w_unrefined']); Cf = np.array(p['C_unrefined']); hgt = Cf[2] + (Rf @ pm.T).T[:, 2]
    und = cv2.undistortPoints(pts2.reshape(-1, 1, 2), Kc, np.array(q[4:8]), P=Kc).reshape(-1, 2); okp = (pm[:, 2] > 0.2) & (hgt > Z0 + 0.25)
    g = np.full((int(1080 * DSo), int(1920 * DSo)), 1e4, np.float32)
    ux = np.clip((und[okp, 0] * DSo).astype(int), 0, g.shape[1] - 1); uy = np.clip((und[okp, 1] * DSo).astype(int), 0, g.shape[0] - 1)
    np.minimum.at(g, (uy, ux), pm[okp, 2].astype(np.float32)); occ[n] = cv2.erode(g, np.ones((9, 9), np.uint8))          # +-4 coarse px ~ +-24 px
log("depth maps", len(maps), "sfm occluder maps", len(occ)); sharp = {}
for n in names:
    g = cv2.cvtColor(cv2.resize(cv2.imread(os.path.join(fdir, n)), (480, 270)), cv2.COLOR_BGR2GRAY); sharp[n] = cv2.Laplacian(g, cv2.CV_64F).var()
sref = np.percentile(list(sharp.values()), 75); store = {}
for k, n in enumerate(names):
    p = poses[n]; q = p['params']; Rc2w = np.array(p['R_c2w_unrefined']); C = np.array(p['C_unrefined']); Wi, Hi = p['W'], p['H']
    if C[2] < Z0 + 0.4: continue
    ci = cand(C)
    if len(ci) == 0: continue
    xc = (X[ci] - C.astype(np.float32)) @ Rc2w.astype(np.float32); z = xc[:, 2]; m0 = z > 0.2; u = np.where(m0, q[0] * xc[:, 0] / np.maximum(z, 1e-3) + q[2], -1); v = np.where(m0, q[1] * xc[:, 1] / np.maximum(z, 1e-3) + q[3], -1)
    m = m0 & (u > 0.02 * Wi) & (u < 0.98 * Wi) & (v > 0.02 * Hi) & (v < 0.98 * Hi); ii_ = np.where(m)[0]
    if len(ii_) == 0: continue
    d = X[ci[ii_]] - C.astype(np.float32); dist = np.linalg.norm(d, axis=1); cosv = (C[2] - Z0) / dist; zi, ui, vi = z[ii_], u[ii_], v[ii_]
    dm = maps[n][np.clip((vi * DS).astype(int), 0, maps[n].shape[0] - 1), np.clip((ui * DS).astype(int), 0, maps[n].shape[1] - 1)]
    keep = (cosv > float(os.environ.get('COSMIN', 0.26))) & (zi <= dm + 0.07 + 0.03 * zi)
    if n in occ:
        do = occ[n][np.clip((vi * DSo).astype(int), 0, occ[n].shape[0] - 1), np.clip((ui * DSo).astype(int), 0, occ[n].shape[1] - 1)]; keep &= zi <= do + 0.35 + 0.05 * zi
    ii_, zi, ui, vi, cosv, dist = ii_[keep], zi[keep], ui[keep], vi[keep], cosv[keep], dist[keep]
    if len(ii_) < 200: continue
    edge = np.minimum.reduce([ui / Wi, 1 - ui / Wi, (vi / Hi) * 1.78, (1 - vi / Hi) * 1.78]); e = np.clip(edge / 0.25, 0, 1); e = e * e * (3 - 2 * e)
    w = p.get('wtier', 1.0) * (cosv ** 2) * (1 / (1 + (dist / 3.5) ** 2)) * e * np.clip(sharp[n] / sref, 0.05, 1) ** 1.5
    img = cv2.imread(os.path.join(fdir, n)); Kc = np.array([[q[0], 0, q[2]], [0, q[1], q[3]], [0, 0, 1.]]); und = cv2.undistort(img, Kc, np.array(q[4:8]), None, Kc)
    nn = len(ui); pad = (-nn) % 1024; mu = np.pad(ui.astype(np.float32), (0, pad)).reshape(-1, 1024); mv = np.pad(vi.astype(np.float32), (0, pad)).reshape(-1, 1024)
    col = cv2.remap(und, mu, mv, cv2.INTER_LINEAR).reshape(-1, 3)[:nn]; br = col.mean(1); okc = (br > 12) & (br < 232); sky = (col[:, 0].astype(int) > col[:, 2].astype(int) + 25) & (br > 150)          # blown-out / bluish-bright = sky seen through openings
    okc &= ~sky
    if okc.sum() < 200: continue
    store[n] = (ci[ii_][okc].astype(np.int32), col[okc], w[okc].astype(np.float32), p.get('tier', 'A') == 'A')
    if k % 150 == 0: log(f"cam {k}/{len(names)} samples {len(ii_)}")
log("sampled cameras", len(store), "total samples %.0f M" % (sum(len(v[0]) for v in store.values()) / 1e6))
A = np.zeros((N, 3), np.float32); Wt = np.zeros(N, np.float32)
for n, (idx, col, w, ta) in store.items(): A[idx] += w[:, None] * col; Wt[idx] += w
avg = A / np.maximum(Wt, 1e-6)[:, None]; gains = {}
for it in range(2):
    for n, (idx, col, w, ta) in store.items():
        g0 = gains.get(n, np.ones(3)); sel = (Wt[idx] > 1.5 * w) & (col.min(1) > 15) & (col.max(1) < 240)
        if sel.sum() > 200: gains[n] = np.clip(g0 * np.median(avg[idx[sel]] / np.maximum(col[sel] * g0, 1), axis=0), 0.7, 1.4)
        else: gains[n] = g0
    A[:] = 0; Wt[:] = 0
    for n, (idx, col, w, ta) in store.items(): A[idx] += w[:, None] * col * gains[n]; Wt[idx] += w
    avg = A / np.maximum(Wt, 1e-6)[:, None]
# ---- photometric consensus: seed with the best view, keep agreeing samples, need >= MINV agreeing views ----
TAU = float(os.environ.get('TAU', 32)); MINV = int(os.environ.get('MINV', 2)); MINFR = float(os.environ.get('MINFR', 0.35))
ids = np.concatenate([v[0] for v in store.values()]); cols = np.concatenate([(v[1] * gains[k]).astype(np.float32) for k, v in store.items()]); ws = np.concatenate([v[2] for v in store.values()]); ta = np.concatenate([np.full(len(v[0]), v[3]) for v in store.values()])
o = np.lexsort((-ws, ids)); ids, cols, ws, ta = ids[o], cols[o], ws[o], ta[o]; uq, first, cnt = np.unique(ids, return_index=True, return_counts=True); grp = np.repeat(np.arange(len(uq)), cnt)
mu = cols[first].copy()                                                     # seed = highest-weight sample of each texel
for it in range(3):
    d = np.linalg.norm(cols - mu[grp], axis=1); keep = d < TAU; wk = ws * keep
    sw = np.add.reduceat(wk, first); sc = np.add.reduceat(wk[:, None] * cols, first, axis=0); mu = np.where(sw[:, None] > 0, sc / np.maximum(sw, 1e-9)[:, None], mu)
nk = np.add.reduceat(keep.astype(np.int32), first); fr = sw / np.maximum(np.add.reduceat(ws, first), 1e-9); good = (nk >= MINV) & (fr >= MINFR)
wA = np.add.reduceat(ws * keep * ta, first); goodA = good & (wA > 0)
log("texels with samples", len(uq), "consistent", int(good.sum()), "(%.1f%%)" % (100 * good.mean()))
tex_good = tex[uq[good]]; cov = np.zeros(N, bool); cov[uq[good]] = True; covA = np.zeros(N, bool); covA[uq[goodA]] = True
M2 = np.zeros(H * W, np.float32); M2[tex_good] = 1; M2 = M2.reshape(H, W); res = np.zeros((H * W, 3), np.float32); res[tex_good] = mu[good]; res = res.reshape(H, W, 3)
dd = cv2.GaussianBlur(M2, (0, 0), 2.0); res = cv2.GaussianBlur(res * M2[..., None], (0, 0), 2.0) / np.maximum(dd, 1e-4)[..., None]; res = np.where(M2[..., None] > 0, 0.5 * res + 0.5 * (mu.mean(0) * 0 + res), res)
valid = (M2 > 0).astype(np.uint8); fill_med = np.median(res[valid > 0], axis=0)
# small gaps -> inpaint; remaining uncovered plate texels -> median floor colour
plate_m = inplate.astype(np.uint8); gap = ((plate_m > 0) & (valid == 0)).astype(np.uint8); distv = cv2.distanceTransform(1 - valid, cv2.DIST_L2, 3); small = (gap > 0) & (distv <= 8)
inp = cv2.inpaint(np.clip(res, 0, 255).astype(np.uint8), small.astype(np.uint8) * 255, 4, cv2.INPAINT_TELEA) if small.any() else res.astype(np.uint8)
final = np.where(valid[..., None] > 0, res, inp.astype(np.float32)); rest = (plate_m > 0) & (valid == 0) & (~small); final[rest] = fill_med
final[plate_m == 0] = fill_med                                                           # outside plate: flat
cv2.imwrite(os.path.join(out, 'floor_texture.png'), np.clip(final, 0, 255).astype(np.uint8))
kind = np.zeros(H * W, np.uint8); kind[tex[cov & covA]] = 255; kind[tex[cov & ~covA]] = 128; kind[tex[~cov]] = 60; kind = kind.reshape(H, W); kind[small] = np.where(kind[small] == 60, 90, kind[small])
cv2.imwrite(os.path.join(out, 'floor_coverage.png'), kind)
st = {'grid_W': W, 'grid_H': H, 'texel_cm': ts * 100, 'x0': gx0, 'y1': gy1, 'z0': Z0, 'plate_m2': N * ts * ts, 'seen_m2': float(cov.sum() * ts * ts), 'seen_pct': float(100 * cov.mean()), 'verified_pose_m2': float((cov & covA).sum() * ts * ts), 'cameras': len(store), 'fill_rgb': fill_med.tolist()}
json.dump(st, open(os.path.join(out, 'floor_stats.json'), 'w'), indent=1); log(st)
