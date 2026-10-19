"""Add photogrammetry / SLAM-reconstruction imperfections to a copy of the Floor18 model and export a GLB.
usage (bpy python): photogrammetry_effect.py in.blend out_dir [seed=20261020]
Effects: global drift (yaw twist, scale, bowl) + low-frequency bowing + mid-frequency waviness + vertex noise + per-wall misregistration
         + missing patches + texture exposure/tint/grain/blur/ghosting/UV misalignment."""
import bpy, bmesh, sys, os, json, time, numpy as np
from mathutils import Matrix
src, out = sys.argv[1], sys.argv[2]; seed = int(sys.argv[3]) if len(sys.argv) > 3 else 20261020; os.makedirs(out, exist_ok=True)
rng = np.random.default_rng(seed); t0 = time.time(); log = lambda *a: print("[%4.0fs]" % (time.time() - t0), *a, flush=True)
P = dict(seed=seed, max_edge_walls=0.14, max_edge_floor=0.35, low_amp=0.022, low_wavelength=(3.0, 9.0), mid_amp=0.006, mid_wavelength=(0.4, 1.6), noise_mm=3.0,
         twist_deg=0.4, scale_err=0.0025, bowl_cm=1.5, wall_tilt_deg=0.25, wall_offset_cm=0.8, hole_prob=0.35, hole_radius=(0.08, 0.35), speckle=0.004,
         tex_gain_sigma=0.07, tex_tint_sigma=0.025, grain=3.5, blur_patches=150, ghost_prob=0.3, uv_jitter_px=2.0, jpeg_quality=72)
bpy.ops.wm.open_mainfile(filepath=src)
ms = [o for o in bpy.data.objects if o.type == 'MESH']
# ---------------- 1) adaptive subdivision (keeps UVs) ----------------
def subdivide(o, max_len):
    bm = bmesh.new(); bm.from_mesh(o.data)
    for _ in range(8):
        long = [e for e in bm.edges if e.calc_length() > max_len]
        if not long or len(bm.faces) > 30000: break
        bmesh.ops.subdivide_edges(bm, edges=long, cuts=1, use_grid_fill=True, use_single_edge=False)
    bmesh.ops.triangulate(bm, faces=bm.faces[:]); bm.to_mesh(o.data); bm.free(); o.data.update()
nbefore = sum(len(o.data.polygons) for o in ms)
for o in ms:
    isfloor = o.name.startswith(('Flat2_', 'Flat1m_', 'Floor_Drone_Ground'))
    if o.name.startswith(('Flat2_', 'Flat1m_')): continue                         # GLB floors are already dense
    subdivide(o, P['max_edge_floor'] if isfloor else P['max_edge_walls'])
log("faces %d -> %d" % (nbefore, sum(len(o.data.polygons) for o in ms)))
# ---------------- 2) displacement fields (functions of WORLD position so coincident surfaces move together) ----------------
def waves(n, wl, amp, rng):
    d = rng.normal(size=(n, 3)); d /= np.linalg.norm(d, axis=1, keepdims=True); lam = rng.uniform(*wl, size=n); ph = rng.uniform(0, 2 * np.pi, size=n); a = rng.uniform(.5, 1.0, size=n) * amp / np.sqrt(n / 2.); return d, lam, ph, a
LOW = [waves(7, P['low_wavelength'], P['low_amp'], rng) for _ in range(3)]; MID = [waves(10, P['mid_wavelength'], P['mid_amp'], rng) for _ in range(3)]
def field(W, comps):
    return np.stack([sum(a[i] * np.sin(2 * np.pi * (W @ d[i]) / lam[i] + ph[i]) for i in range(len(a))) for (d, lam, ph, a) in comps], 1)
def hash_noise(W, mm):
    q = np.floor(W / 0.01); return np.stack([np.modf(np.sin(q @ v) * 43758.5453)[0] * 2 - 1 for v in ([12.9898, 78.233, 37.719], [39.346, 11.135, 83.155], [73.156, 52.235, 9.151])], 1) * (mm / 1000.) * 0.6
c0 = np.array([-1.5, 0.1, 0.0]); th = np.radians(P['twist_deg'])
def global_drift(W):
    r = W - c0; ang = th * (r[:, 0] / 18.0); c, s = np.cos(ang), np.sin(ang); x = c * r[:, 0] - s * r[:, 1]; y = s * r[:, 0] + c * r[:, 1]
    out = np.stack([x, y, r[:, 2]], 1) * (1 + P['scale_err']); out[:, 2] += (P['bowl_cm'] / 100.) * ((r[:, 0] ** 2 + r[:, 1] ** 2) / 400.); return out + c0 - W
stats = {'objects': 0, 'disp_rms_m': 0.0, 'disp_max_m': 0.0}; allD = []
for o in ms:
    me = o.data; n = len(me.vertices)
    if n == 0: continue
    co = np.zeros(n * 3, np.float32); me.vertices.foreach_get('co', co); co = co.reshape(-1, 3).astype(np.float64); M = np.array(o.matrix_world); Mi = np.linalg.inv(M)
    W = (M[:3, :3] @ co.T).T + M[:3, 3]; D = global_drift(W) + field(W, LOW) + field(W, MID) + hash_noise(W, P['noise_mm'])
    # per-object misregistration: small tilt about a random in-plane axis + normal offset
    ctr = W.mean(0); ax = rng.normal(size=3); ax /= np.linalg.norm(ax); ang = np.radians(rng.normal(0, P['wall_tilt_deg'])); K = np.array([[0, -ax[2], ax[1]], [ax[2], 0, -ax[0]], [-ax[1], ax[0], 0]]); Rm = np.eye(3) + np.sin(ang) * K + (1 - np.cos(ang)) * K @ K
    D += (Rm @ (W - ctr).T).T + ctr - W + rng.normal(0, P['wall_offset_cm'] / 100. / 2, size=3)
    if o.name.startswith(('Flat2_', 'Flat1m_', 'Floor_Drone_Ground')): D[:, :2] *= 0.6                  # floors: slightly less in-plane shear
    W2 = W + D; new = (np.linalg.inv(M)[:3, :3] @ W2.T).T + np.linalg.inv(M)[:3, 3]; me.vertices.foreach_set('co', new.astype(np.float32).ravel()); me.update()
    allD.append(np.linalg.norm(D, axis=1)); stats['objects'] += 1
allD = np.concatenate(allD); stats['disp_rms_m'] = float(np.sqrt((allD ** 2).mean())); stats['disp_max_m'] = float(allD.max()); stats['disp_p95_m'] = float(np.percentile(allD, 95)); log("displacement rms %.1f cm  p95 %.1f cm  max %.1f cm" % (100 * stats['disp_rms_m'], 100 * stats['disp_p95_m'], 100 * stats['disp_max_m']))
# ---------------- 3) missing patches (unobserved regions) ----------------
nh = nf0 = 0
for o in ms:
    if o.name.startswith(('Flat2_', 'Flat1m_')) or len(o.data.polygons) < 20: continue
    bm = bmesh.new(); bm.from_mesh(o.data); bm.faces.ensure_lookup_table(); cen = np.array([f.calc_center_median() for f in bm.faces]); kill = np.zeros(len(cen), bool)
    if rng.random() < P['hole_prob']:
        for _ in range(rng.integers(1, 4)):
            seedp = cen[rng.integers(len(cen))]; r = rng.uniform(*P['hole_radius']); kill |= np.linalg.norm(cen - seedp, axis=1) < r * rng.uniform(0.6, 1.2, size=len(cen)); nh += 1
    kill |= rng.random(len(cen)) < P['speckle']
    if kill.any() and kill.sum() < 0.6 * len(cen): nf0 += int(kill.sum()); bmesh.ops.delete(bm, geom=[bm.faces[i] for i in np.where(kill)[0]], context='FACES')
    bm.to_mesh(o.data); bm.free(); o.data.update()
log("holes", nh, "faces removed", nf0)
# ---------------- 4) texture imperfections ----------------
def get_px(img):
    w, h = img.size; a = np.zeros(w * h * 4, np.float32); img.pixels.foreach_get(a); return a.reshape(h, w, 4)[::-1, :, :3].copy()                  # top-down RGB 0..1
def set_px(img, a):
    w, h = img.size; rgba = np.ones((h, w, 4), np.float32); rgba[..., :3] = np.clip(a[::-1], 0, 1); img.pixels.foreach_set(rgba.ravel()); img.pack(); img.update()
import cv2
def islands(imgname, uvname, matname):
    """per-object island = bbox + exact triangle mask (drops degenerate triangles and the flat 'unseen' corner patch)"""
    img = bpy.data.images[imgname]; w, h = img.size; res = []
    for o in ms:
        if not o.data.materials or o.data.materials[0].name != matname or uvname not in o.data.uv_layers: continue
        uv = np.zeros(len(o.data.loops) * 2, np.float32); o.data.uv_layers[uvname].data.foreach_get('uv', uv); uv = uv.reshape(-1, 2)
        if len(uv) < 3: continue
        px = np.stack([uv[:, 0] * w, (1 - uv[:, 1]) * h], 1); tri = px.reshape(-1, 3, 2)
        area = 0.5 * np.abs((tri[:, 1, 0] - tri[:, 0, 0]) * (tri[:, 2, 1] - tri[:, 0, 1]) - (tri[:, 1, 1] - tri[:, 0, 1]) * (tri[:, 2, 0] - tri[:, 0, 0]))
        corner = (tri[:, :, 0] > w - 28).all(1) & (tri[:, :, 1] > h - 28).all(1); ok = (area > 0.5) & ~corner
        if not ok.any(): continue
        T = tri[ok]; x0, x1 = int(max(T[..., 0].min() - 1, 0)), int(min(T[..., 0].max() + 2, w)); y0, y1 = int(max(T[..., 1].min() - 1, 0)), int(min(T[..., 1].max() + 2, h))
        if x1 - x0 < 6 or y1 - y0 < 6 or (x1 - x0) * (y1 - y0) > 0.25 * w * h: continue
        m = np.zeros((y1 - y0, x1 - x0), np.uint8); cv2.fillPoly(m, [np.round(t - [x0, y0]).astype(np.int32) for t in T], 1); m = cv2.dilate(m, np.ones((3, 3), np.uint8)); res.append((o, x0, x1, y0, y1, m.astype(bool)))
    return res
tex_log = {}
for imgname, uvname, matname in (('Floor18_baked', 'BakedUV', 'Floor18_BakedTexture'), ('Flat_GLB_atlas', 'GLB_UV', 'Flat_GLB_AtlasMaterial')):
    img = bpy.data.images[imgname]; a = get_px(img); isl = islands(imgname, uvname, matname); cnt = 0
    for o, x0, x1, y0, y1, msk in isl:
        reg = a[y0:y1, x0:x1].copy(); g = rng.normal(1, P['tex_gain_sigma']); tint = rng.normal(1, P['tex_tint_sigma'], size=3); ct = rng.normal(1, 0.06); mean = reg.mean()
        reg = (reg - mean) * ct + mean; reg = reg * g * tint
        if rng.random() < P['ghost_prob']:
            sh = rng.integers(5, 13) * rng.choice([-1, 1]); sv = rng.integers(0, 7) * rng.choice([-1, 1]); reg = 0.72 * reg + 0.28 * np.roll(np.roll(reg, sh, axis=1), sv, axis=0)       # double-image from mis-registered views
        a[y0:y1, x0:x1][msk] = reg[msk]; cnt += 1
        if P['uv_jitter_px'] > 0:                                                                                                # texture swim: shift the island's UVs a couple of pixels
            jx, jy = rng.uniform(-1, 1, 2) * P['uv_jitter_px'] / img.size[0]; lay = o.data.uv_layers[uvname].data; uv = np.zeros(len(lay) * 2, np.float32); lay.foreach_get('uv', uv); uv = uv.reshape(-1, 2) + [jx, jy]; lay.foreach_set('uv', uv.ravel())
    for _ in range(P['blur_patches']):                                                                                          # blurred patches (motion blur / low-res views)
        w_, h_ = rng.integers(30, 220, 2); x = rng.integers(0, a.shape[1] - w_); y = rng.integers(0, a.shape[0] - h_); sg = rng.uniform(1.5, 4.0); a[y:y + h_, x:x + w_] = cv2.GaussianBlur(a[y:y + h_, x:x + w_], (0, 0), sg)
    a += rng.normal(0, P['grain'] / 255., a.shape).astype(np.float32); set_px(img, a); tex_log[imgname] = {'islands_modified': cnt}; log("texture", imgname, "islands", cnt)
img = bpy.data.images['Floor_drone_ground']; a = get_px(img); h, w = a.shape[:2]                                                    # ground: smooth exposure field + blur patches + grain
lowf = cv2.resize(rng.normal(1, 0.08, (h // 160 + 2, w // 160 + 2)).astype(np.float32), (w, h), interpolation=cv2.INTER_CUBIC); a *= lowf[..., None]
for _ in range(80):
    w_, h_ = rng.integers(30, 200, 2); x = rng.integers(0, w - w_); y = rng.integers(0, h - h_); a[y:y + h_, x:x + w_] = cv2.GaussianBlur(a[y:y + h_, x:x + w_], (0, 0), rng.uniform(1.5, 4))
a += rng.normal(0, P['grain'] / 255., a.shape).astype(np.float32); set_px(img, a)
for nm in [i.name for i in bpy.data.images if i.name.startswith('floor_tex_')]:
    im = bpy.data.images[nm]; b = get_px(im); b += rng.normal(0, P['grain'] / 255., b.shape).astype(np.float32); b *= rng.normal(1, 0.05); set_px(im, b)
log("textures done")
# ---------------- 5) save the copy + export ----------------
for o in list(bpy.data.objects):
    if o.type == 'CAMERA': o.hide_render = True
bpy.ops.wm.save_as_mainfile(filepath=os.path.join(out, 'Floor18_photogrammetry_copy.blend')); log("saved blend copy")
bpy.ops.object.select_all(action='DESELECT')
for o in bpy.data.objects: o.select_set(o.type == 'MESH')
bpy.ops.export_scene.gltf(filepath=os.path.join(out, 'photogrammetry_output.glb'), export_format='GLB', use_selection=True, export_image_format='JPEG', export_jpeg_quality=P['jpeg_quality'], export_cameras=False, export_animations=False, export_apply=False, export_yup=True)
json.dump({'params': {k: (list(v) if isinstance(v, tuple) else v) for k, v in P.items()}, 'displacement': stats, 'textures': tex_log}, open(os.path.join(out, 'photogrammetry_params.json'), 'w'), indent=1); log("exported glb")
