import trimesh, numpy as np, cv2, json
from scipy.signal import fftconvolve
s = trimesh.load('flat2.glb', force='scene'); meshes = [g for g in s.geometry.values()]
# glTF is Y-up: (x, y, z) -> Blender Z-up (x, -z, y)
def to_blender(v): return np.stack([v[:, 0], -v[:, 2], v[:, 1]], 1)
V = np.vstack([to_blender(g.vertices) for g in meshes]); off = np.cumsum([0] + [len(g.vertices) for g in meshes]); F = np.vstack([g.faces + o for g, o in zip(meshes, off[:-1])])
print("GLB bbox (blender axes)", V.min(0).round(2), V.max(0).round(2))
np.savez('glb_blender.npz', V=V, F=F)
def wall_lines(V, F, tol=0.05):
    n = np.cross(V[F[:, 1]] - V[F[:, 0]], V[F[:, 2]] - V[F[:, 0]]); ln = np.linalg.norm(n, axis=1); ok = ln > 1e-9; n[ok] /= ln[ok, None]
    vert = ok & (np.abs(n[:, 2]) < tol); segs = []
    for f in F[vert]:
        p = V[f][:, :2]; i = np.argmax(np.linalg.norm(p[:, None] - p[None], axis=2).max(1)); j = np.argmax(np.linalg.norm(p - p[i], axis=1)); segs.append((p[i], p[j]))
    return segs
glb_segs = wall_lines(V, F)
ud = np.load('user_model.npz'); UV_, UF = ud['V'], ud['F']; mod_segs = wall_lines(UV_, UF)
print("segs glb", len(glb_segs), "model", len(mod_segs))
RES = 0.05; lo = UV_[:, :2].min(0) - 14; hi = UV_[:, :2].max(0) + 14; Wg, Hg = np.ceil((hi - lo) / RES).astype(int)
img = np.zeros((Hg, Wg), np.uint8)
for a, b in mod_segs: cv2.line(img, tuple(((a - lo) / RES).round().astype(int)), tuple(((b - lo) / RES).round().astype(int)), 255, 1)
DT = cv2.distanceTransform(255 - img, cv2.DIST_L2, 3) * RES; Fimg = np.exp(-(DT / 0.10) ** 2).astype(np.float32)
def raster(segs, T):
    P = [(T(a), T(b)) for a, b in segs]; allp = np.array([p for ab in P for p in ab]); mn = allp.min(0)
    sz = np.ceil((allp.max(0) - mn) / RES).astype(int) + 3; im = np.zeros((sz[1], sz[0]), np.float32)
    for a, b in P: cv2.line(im, tuple(((a - mn) / RES).round().astype(int) + 1), tuple(((b - mn) / RES).round().astype(int) + 1), 1.0, 1)
    return im, mn - RES
res = []
for mirror in (False, True):
    for k in range(4):
        th = np.radians(90 * k); c, sn = np.cos(th), np.sin(th)
        def T(p, mirror=mirror, c=c, sn=sn):
            q = np.array([-p[0], p[1]]) if mirror else np.array(p, float); return np.array([c * q[0] - sn * q[1], sn * q[0] + c * q[1]])
        H, mn = raster(glb_segs, T)
        if H.shape[0] >= Hg or H.shape[1] >= Wg: continue
        score = fftconvolve(Fimg, H[::-1, ::-1], mode='valid') / H.sum()
        for _ in range(3):
            kk = np.unravel_index(np.argmax(score), score.shape); res.append((float(score[kk]), mirror, 90 * k, (lo[0] + kk[1] * RES - mn[0], lo[1] + kk[0] * RES - mn[1]))); score[max(0, kk[0] - 40):kk[0] + 40, max(0, kk[1] - 40):kk[1] + 40] = -1
res.sort(key=lambda r: -r[0])
for r in res[:8]: print("score %.3f mirror=%s rot=%d  shift=(%.2f, %.2f)" % (r[0], r[1], r[2], r[3][0], r[3][1]))
json.dump([{'score': r[0], 'mirror': r[1], 'rot': r[2], 'shift': list(map(float, r[3]))} for r in res[:10]], open('match_result.json', 'w'))
