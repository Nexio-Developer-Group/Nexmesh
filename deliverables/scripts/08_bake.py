"""Texture bake. usage: 08_bake.py poses.json model.npz frames_dir out_dir [atlas=4096] [frame_scale=1.0] [max_cams=0]
poses.json: per image {R_c2w, C, params(fx,fy,cx,cy,k1,k2,p1,p2 at W,H)} in Blender world. frame_scale: frames_dir image width / pose W."""
import sys, os, json, time, numpy as np, cv2, trimesh
sys.path.insert(0, os.path.dirname(__file__)); from common import *
poses = json.load(open(sys.argv[1])); mesh, md = load_model(sys.argv[2]); fdir = sys.argv[3]; out = sys.argv[4]
ATL = int(sys.argv[5]) if len(sys.argv) > 5 else 4096; FS = float(sys.argv[6]) if len(sys.argv) > 6 else 1.0; MAXC = int(sys.argv[7]) if len(sys.argv) > 7 else 0
os.makedirs(out, exist_ok=True); t0 = time.time(); log = lambda *a: print("[%5.0fs]" % (time.time() - t0), *a, flush=True)
V, F, OBJ = mesh.vertices, mesh.faces, md['OBJ']; NT = len(F); nrm = mesh.face_normals; ren = Renderer(mesh, OBJ)
names = sorted(poses); names = names[:MAXC] if MAXC else names
# ---------- A. depth/tri maps per camera (1/6 scale) ----------
DS = 1 / 6.; maps = {}; seen_px = np.zeros(NT, np.int64)
for n in names:
    p = poses[n]; q = p['params']; K = np.array([[q[0], 0, q[2]], [0, q[1], q[3]], [0, 0, 1.]])
    dep, tri = ren.render(K, p['W'], p['H'], np.array(p['R_c2w_unrefined']), np.array(p['C_unrefined']), scale=DS)
    maps[n] = cv2.erode(np.where(np.isfinite(dep), dep, 1e4).astype(np.float32), np.ones((3, 3), np.uint8))
    t = tri[tri >= 0]; seen_px += np.bincount(t, minlength=NT)
seen = seen_px >= 6; log("depth maps done; tris seen", seen.sum(), "of", NT)
# ---------- B. atlas ----------
objs = np.unique(OBJ[seen]); pack = []
for o in objs:
    fi = np.where((OBJ == o))[0]; vi = np.unique(F[fi].ravel()); n0 = nrm[fi[0]]
    ex = np.cross([0, 0, 1.], n0); ex = ex / np.linalg.norm(ex) if np.linalg.norm(ex) > 1e-6 else np.array([1., 0, 0]); ey = np.cross(n0, ex)
    uv = np.stack([V[vi] @ ex, V[vi] @ ey], 1); lo = uv.min(0); size = uv.max(0) - lo
    pack.append((o, fi, vi, uv - lo, size))
def try_pack(ts):
    pad = 3; x = y = 0; rowh = 0; place = {}
    for o, fi, vi, uv, size in sorted(pack, key=lambda p: -p[4][1]):
        w, h = int(np.ceil(size[0] / ts)) + 2 * pad, int(np.ceil(size[1] / ts)) + 2 * pad
        if x + w > ATL: x = 0; y += rowh; rowh = 0
        if y + h > ATL - 24 or w > ATL: return None
        place[o] = (x + pad, y + pad); x += w; rowh = max(rowh, h)
    return place
ts = 0.008
while (place := try_pack(ts)) is None: ts *= 1.04
log("atlas texel size %.2f cm" % (ts * 100))
UV = np.zeros((NT, 3, 2)); pos2 = {}
for o, fi, vi, uv, size in pack:
    ox, oy = place[o]; pix = uv / ts + [ox, oy]; m = dict(zip(vi, pix))
    for f in fi: UV[f] = [m[k] for k in F[f]]
flat = (ATL - 12, ATL - 12)
for f in np.where(~seen)[0]: UV[f] = [flat] * 3                         # unseen faces -> flat patch
# rasterise seen triangles -> texel arrays
tid = []; bar = []; tex = []
for f in np.where(seen)[0]:
    a, b, c = UV[f]; x0, x1 = int(np.floor(min(a[0], b[0], c[0]))), int(np.ceil(max(a[0], b[0], c[0]))); y0, y1 = int(np.floor(min(a[1], b[1], c[1]))), int(np.ceil(max(a[1], b[1], c[1])))
    xs, ys = np.meshgrid(np.arange(x0, x1 + 1), np.arange(y0, y1 + 1)); px = np.stack([xs.ravel() + .5, ys.ravel() + .5], 1)
    T = np.array([[a[0] - c[0], b[0] - c[0]], [a[1] - c[1], b[1] - c[1]]]); det = np.linalg.det(T)
    if abs(det) < 1e-9: continue
    l = (np.linalg.inv(T) @ (px - c).T).T; l = np.c_[l, 1 - l.sum(1)]; ok = (l > -0.002).all(1)
    tid.append(np.full(ok.sum(), f)); bar.append(l[ok]); tex.append(ys.ravel()[ok] * ATL + xs.ravel()[ok])
tid = np.concatenate(tid); bar = np.concatenate(bar).astype(np.float32); tex = np.concatenate(tex)
X = np.einsum('ij,ijk->ik', bar, V[F[tid]]).astype(np.float32); NN = nrm[tid].astype(np.float32); N = len(tid); log("texels", N)
np.savez(os.path.join(out, 'atlas_uv.npz'), UV=UV, seen=seen, ATL=ATL)
# ---------- C. project, test, sample ----------
store = {}; sharp = {}
for n in names:
    g = cv2.cvtColor(cv2.resize(cv2.imread(os.path.join(fdir, n)), (480, 270)), cv2.COLOR_BGR2GRAY); sharp[n] = cv2.Laplacian(g, cv2.CV_64F).var()
sref = np.percentile(list(sharp.values()), 75)
for k, n in enumerate(names):
    p = poses[n]; q = p['params']; Rc2w = np.array(p['R_c2w_unrefined']); C = np.array(p['C_unrefined']); W, H = p['W'], p['H']
    xc = (X - C.astype(np.float32)) @ Rc2w.astype(np.float32); z = xc[:, 2]; ok = z > 0.2
    u = np.where(ok, q[0] * xc[:, 0] / np.maximum(z, 1e-3) + q[2], -1); v = np.where(ok, q[1] * xc[:, 1] / np.maximum(z, 1e-3) + q[3], -1)
    m = ok & (u > 0.02 * W) & (u < 0.98 * W) & (v > 0.02 * H) & (v < 0.98 * H)
    idx = np.where(m)[0]
    if len(idx) == 0: continue
    d = (X[idx] - C.astype(np.float32)); dist = np.linalg.norm(d, axis=1); cosv = -np.einsum('ij,ij->i', NN[idx], d) / dist
    zi, ui, vi = z[idx], u[idx], v[idx]; dm = maps[n][np.clip((vi * DS).astype(int), 0, maps[n].shape[0] - 1), np.clip((ui * DS).astype(int), 0, maps[n].shape[1] - 1)]
    keep = (cosv > 0.2) & (zi <= dm + 0.07 + 0.03 * zi); idx, zi, ui, vi, cosv, dist = idx[keep], zi[keep], ui[keep], vi[keep], cosv[keep], dist[keep]
    if len(idx) < 50: continue
    edge = np.minimum.reduce([ui / W, 1 - ui / W, (vi / H) * 1.78, (1 - vi / H) * 1.78]); e = np.clip(edge / 0.25, 0, 1); e = e * e * (3 - 2 * e)
    w = (cosv ** 2) * (1 / (1 + (dist / 4) ** 2)) * e * np.clip(sharp[n] / sref, 0.05, 1) ** 1.5
    img = cv2.imread(os.path.join(fdir, n)); Kc = np.array([[q[0] * FS, 0, q[2] * FS], [0, q[1] * FS, q[3] * FS], [0, 0, 1.]])
    und = cv2.undistort(img, Kc, np.array(q[4:8]), None, Kc)
    nn = len(ui); pad = (-nn) % 1024; mu = np.pad((ui * FS).astype(np.float32), (0, pad)).reshape(-1, 1024); mv = np.pad((vi * FS).astype(np.float32), (0, pad)).reshape(-1, 1024)
    col = cv2.remap(und, mu, mv, cv2.INTER_LINEAR).reshape(-1, 3)[:nn]
    store[n] = (idx.astype(np.int32), col, w.astype(np.float32))
    if k % 100 == 0: log(f"cam {k}/{len(names)} samples {len(idx)}")
log("sampling done; stored", len(store))
# ---------- D. exposure gains (relative to weighted mean of all views) ----------
A = np.zeros((N, 3), np.float32); Wt = np.zeros(N, np.float32)
for n, (idx, col, w) in store.items(): A[idx] += w[:, None] * col; Wt[idx] += w
avg = A / np.maximum(Wt, 1e-6)[:, None]; gains = {}
for n, (idx, col, w) in store.items():
    sel = (Wt[idx] > 1.5 * w) & (col.min(1) > 15) & (col.max(1) < 240)
    gains[n] = np.clip(np.median(avg[idx[sel]] / np.maximum(col[sel], 1), axis=0), 0.7, 1.4) if sel.sum() > 200 else np.ones(3)
# a second iteration of gains with the corrected average
A[:] = 0; Wt[:] = 0
for n, (idx, col, w) in store.items(): A[idx] += w[:, None] * col * gains[n]; Wt[idx] += w
avg = A / np.maximum(Wt, 1e-6)[:, None]
for n, (idx, col, w) in store.items():
    sel = (Wt[idx] > 1.5 * w) & (col.min(1) > 15) & (col.max(1) < 240)
    if sel.sum() > 200: gains[n] = np.clip(gains[n] * np.median(avg[idx[sel]] / np.maximum(col[sel] * gains[n], 1), axis=0), 0.7, 1.4)
json.dump({n: list(map(float, g)) for n, g in gains.items()}, open(os.path.join(out, 'exposure_gains.json'), 'w'))
# ---------- E. two-band blend ----------
A1 = np.zeros((N, 3), np.float32); W1 = np.zeros(N, np.float32); A8 = np.zeros((N, 3), np.float32); W8 = np.zeros(N, np.float32)
for n, (idx, col, w) in store.items():
    c = col * gains[n]; A1[idx] += w[:, None] * c; W1[idx] += w; w8 = (w / max(w.max(), 1e-9)) ** 6 * w.max(); A8[idx] += w8[:, None] * c; W8[idx] += w8
cov = W1 > 1e-4; M = np.zeros(ATL * ATL, np.float32); M[tex[cov]] = 1
mean1 = np.zeros((ATL * ATL, 3), np.float32); mean1[tex[cov]] = (A1[cov] / W1[cov, None]); mean8 = np.zeros_like(mean1); mean8[tex[cov]] = A8[cov] / np.maximum(W8[cov, None], 1e-9)
M2 = M.reshape(ATL, ATL); m1 = mean1.reshape(ATL, ATL, 3); m8 = mean8.reshape(ATL, ATL, 3)
def mblur(img, s): d = cv2.GaussianBlur(M2, (0, 0), s); return cv2.GaussianBlur(img * M2[..., None], (0, 0), s) / np.maximum(d, 1e-4)[..., None]
low = mblur(m1, 6); high = m8 - mblur(m8, 6); res = np.clip(low + high, 0, 255) * M2[..., None]
# ---------- F. gutters, small-hole fill, flat colour for the rest ----------
valid = (M2 > 0).astype(np.uint8)
inv = (1 - valid).astype(np.uint8); dist, lab = cv2.distanceTransformWithLabels(inv, cv2.DIST_L2, 3, labelType=cv2.DIST_LABEL_PIXEL)
ys, xs = np.where(valid > 0); labmap = np.zeros(lab.max() + 1, np.int64); labmap[lab[ys, xs]] = ys * ATL + xs
src = labmap[lab]; filled = res.reshape(-1, 3)[src.ravel()].reshape(ATL, ATL, 3)
gut = (dist <= 4) & (valid == 0); final = res.copy(); final[gut] = filled[gut]
FLAT = np.array([70, 62, 55], np.float32)[::-1] * 0 + np.array([110, 110, 120], np.float32)      # BGR flat colour for never-seen
nodata = ~((valid > 0) | gut); final[nodata] = FLAT
cv2.imwrite(os.path.join(out, 'texture_%d.png' % ATL), final.astype(np.uint8))
cv2.imwrite(os.path.join(out, 'coverage_mask_%d.png' % ATL), (valid * 255).astype(np.uint8))
area_tex = np.zeros(NT); np.add.at(area_tex, tid[cov], 1)
tri_area = mesh.area_faces; tri_cov = np.zeros(NT); np.add.at(tri_cov, tid, 1)
frac_tri = np.zeros(NT); frac_tri[seen] = (area_tex[seen] / np.maximum(tri_cov[seen], 1))
tot_area = tri_area.sum(); cov_area = (tri_area * frac_tri).sum()
stats = {'texel_size_cm': ts * 100, 'faces_total': int(NT), 'faces_seen': int(seen.sum()), 'surface_area_total_m2': float(tot_area), 'surface_area_with_real_texture_m2': float(cov_area), 'percent_surface_covered': float(100 * cov_area / tot_area), 'cameras_used': len(store), 'atlas_px_used_valid_pct': float(100 * valid.mean())}
json.dump(stats, open(os.path.join(out, 'bake_stats.json'), 'w'), indent=1); log(stats)
