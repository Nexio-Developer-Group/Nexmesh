"""Transfer GLB (Flat 2) textures into the Floor18 baked atlas for flat 2 and its mirrored twin flat 1; add GLB floors.
usage (bpy python): transfer_glb.py user_floor18.blend flat2.glb placement.json out_dir"""
import sys, os, json, time, numpy as np, cv2, bpy, trimesh
from trimesh.ray.ray_pyembree import RayMeshIntersector
src, glb, plf, out = sys.argv[1:5]; os.makedirs(out, exist_ok=True); t0 = time.time(); log = lambda *a: print("[%4.0fs]" % (time.time() - t0), *a, flush=True)
PL = json.load(open(plf)); sc = trimesh.load(glb, force='scene')
# ---------- GLB -> Blender axes, per geometry ----------
G = []
for name, g in sc.geometry.items():
    m = g.visual.material; img = getattr(m, 'baseColorTexture', None)
    G.append({'mat': m.name, 'V': np.stack([g.vertices[:, 0], -g.vertices[:, 2], g.vertices[:, 1]], 1), 'F': g.faces.copy(), 'uv': np.array(g.visual.uv), 'img': None if img is None else cv2.cvtColor(np.array(img.convert('RGB')), cv2.COLOR_RGB2BGR), 'floor': 'Floor' in m.name})
log("GLB geoms", len(G), "textured", sum(1 for x in G if x['img'] is not None))
def placed(key):
    p = PL[key]; sh = np.array(p['shift']); mir = p['mirror']; Vs = []; Fs = []; gid = []; UVs = []; off = 0
    for i, x in enumerate(G):
        V = x['V'].copy()
        if mir: V[:, 1] = -V[:, 1]
        V = V + sh; F = x['F'] if not mir else x['F'][:, ::-1]
        Vs.append(V); Fs.append(F + off); gid.append(np.full(len(F), i)); UVs.append(x['uv'][F - 0] if False else x['uv']); off += len(V)
    return np.vstack(Vs), np.vstack(Fs), np.concatenate(gid), np.vstack(UVs)
# ---------- user's blend ----------
bpy.ops.wm.open_mainfile(filepath=src)
im = bpy.data.images['Floor18_baked']; ATL0 = im.size[0]
px = np.zeros(ATL0 * ATL0 * 4, np.float32); im.pixels.foreach_get(px); old_atlas = (px.reshape(ATL0, ATL0, 4)[::-1, :, :3][:, :, ::-1] * 255 + .5).astype(np.uint8).copy()   # BGR, row0 = top
boxes = {'flat2': (-19.7, -8.7, -2.1, 9.7), 'flat1': (-19.7, -8.7, -13.0, -1.2)}
sel = []                                                         # objects to move to the new atlas
for o in bpy.data.objects:
    if o.type != 'MESH' or 'BakedUV' not in o.data.uv_layers or o.name == 'Plane': continue
    me = o.data; v = np.array([tuple(x.co) for x in me.vertices]).reshape(-1, 3); M = np.array(o.matrix_world); v = (M[:3, :3] @ v.T).T + M[:3, 3]; c = v.mean(0)
    if not any(b[0] < c[0] < b[1] and b[2] < c[1] < b[3] for b in boxes.values()): continue
    me.calc_loop_triangles(); n = np.cross(v[[t.vertices[1] for t in me.loop_triangles[:1]]] - v[[t.vertices[0] for t in me.loop_triangles[:1]]], v[[t.vertices[2] for t in me.loop_triangles[:1]]] - v[[t.vertices[0] for t in me.loop_triangles[:1]]])
    ln = np.linalg.norm(n)
    if ln < 1e-12: continue
    n = (n / ln)[0] if n.ndim > 1 else n / ln
    if abs(n[2]) > 0.2: continue                                  # vertical walls only
    sel.append((o, v, n))
log("objects moved to the new atlas:", len(sel), " drone atlas", ATL0)
# ---------- pack islands (planar projection per object) ----------
ATL = 4096; isl = []
for o, v, n in sel:
    ex = np.cross([0, 0, 1.], n); ex /= np.linalg.norm(ex); ey = np.cross(n, ex); uv = np.stack([v @ ex, v @ ey], 1); lo = uv.min(0); isl.append((o, v, n, uv - lo, (uv - lo).max(0)))
def try_pack(ts):
    pad = 3; x = y = 0; rowh = 0; place = {}
    for k in sorted(range(len(isl)), key=lambda k: -isl[k][4][1]):
        w, h = int(np.ceil(isl[k][4][0] / ts)) + 2 * pad, int(np.ceil(isl[k][4][1] / ts)) + 2 * pad
        if w > ATL: return None
        if x + w > ATL: x = 0; y += rowh; rowh = 0
        if y + h > ATL - 24: return None
        place[k] = (x + pad, y + pad); x += w; rowh = max(rowh, h)
    return place
ts = 0.010
while (place := try_pack(ts)) is None: ts *= 1.04
log("new atlas texel size %.2f cm" % (ts * 100))
NA = np.zeros((ATL, ATL, 3), np.uint8); NA[:] = (120, 118, 112)                   # flat grey for faces nobody saw
TW, TU, TO, TUold, TV_ = [], [], [], [], []
for k, (o, v, n, uv, size) in enumerate(isl):
    me = o.data; ox, oy = place[k]; pix = uv / ts + [ox, oy]; o['glb_uv'] = pix.tolist()
    uvl = me.uv_layers['BakedUV'].data
    for t in me.loop_triangles:
        vi = list(t.vertices); TW.append(v[vi]); TU.append(pix[vi]); TUold.append([tuple(uvl[l].uv) for l in t.loops]); TO.append(k); TV_.append(n)
TOk = np.array(TO); TW = np.array(TW); TU = np.array(TU); TUold = np.array(TUold); TUold[..., 1] = 1 - TUold[..., 1]; TUold *= ATL0; Nn = np.array(TV_); log("triangles", len(TW))
# ---------- rasterise ----------
tid, bar, tex = [], [], []; allmask = np.zeros((ATL, ATL), np.uint8)
for f in range(len(TW)):
    a, b, c = TU[f]; x0, x1 = int(np.floor(min(a[0], b[0], c[0]))), int(np.ceil(max(a[0], b[0], c[0]))); y0, y1 = int(np.floor(min(a[1], b[1], c[1]))), int(np.ceil(max(a[1], b[1], c[1])))
    xs, ys = np.meshgrid(np.arange(x0, x1 + 1), np.arange(y0, y1 + 1)); pp = np.stack([xs.ravel() + .5, ys.ravel() + .5], 1)
    T = np.array([[a[0] - c[0], b[0] - c[0]], [a[1] - c[1], b[1] - c[1]]])
    if abs(np.linalg.det(T)) < 1e-9: continue
    l = (np.linalg.inv(T) @ (pp - c).T).T; l = np.c_[l, 1 - l.sum(1)]; ok = (l > -0.002).all(1); xi, yi = xs.ravel()[ok], ys.ravel()[ok]; inb = (xi >= 0) & (xi < ATL) & (yi >= 0) & (yi < ATL)
    allmask[yi[inb], xi[inb]] = 1; tid.append(np.full(inb.sum(), f)); bar.append(l[ok][inb]); tex.append(yi[inb] * ATL + xi[inb])
tid = np.concatenate(tid); bar = np.concatenate(bar); tex = np.concatenate(tex); log("texels", len(tid))
X = np.einsum('ij,ijk->ik', bar, TW[tid]); N = Nn[tid]
# ---------- 1) pre-fill from the drone atlas where that face was seen ----------
def bil(img, x, y):
    h, w = img.shape[:2]; x = np.clip(x - .5, 0, w - 1.001); y = np.clip(y - .5, 0, h - 1.001); x0 = np.floor(x).astype(int); y0 = np.floor(y).astype(int); fx = (x - x0)[:, None]; fy = (y - y0)[:, None]
    return img[y0, x0] * (1 - fx) * (1 - fy) + img[y0, x0 + 1] * fx * (1 - fy) + img[y0 + 1, x0] * (1 - fx) * fy + img[y0 + 1, x0 + 1] * fx * fy
uvold = np.einsum('ij,ijk->ik', bar, TUold[tid]); e1 = TUold[:, 1] - TUold[:, 0]; e2 = TUold[:, 2] - TUold[:, 0]; area_old = np.abs(e1[:, 0] * e2[:, 1] - e1[:, 1] * e2[:, 0]) / 2; seen_old = area_old[tid] > 4.0
colors = np.full((len(tid), 3), (120, 118, 112), np.float32); colors[seen_old] = bil(old_atlas, uvold[seen_old, 0], uvold[seen_old, 1]); src_kind = np.where(seen_old, 1, 0).astype(np.int8)   # 1 = drone, 0 = none
log("pre-filled from drone atlas:", seen_old.sum(), "of", len(seen_old))
# ---------- 2) GLB override ----------
def bilinear(img, u, v):
    h, w = img.shape[:2]; x = np.clip(u, 0, 1) * (w - 1); y = np.clip(1 - v, 0, 1) * (h - 1)
    x0 = np.floor(x).astype(int); y0 = np.floor(y).astype(int); x1 = np.minimum(x0 + 1, w - 1); y1 = np.minimum(y0 + 1, h - 1); fx = (x - x0)[:, None]; fy = (y - y0)[:, None]
    return (img[y0, x0] * (1 - fx) * (1 - fy) + img[y0, x1] * fx * (1 - fy) + img[y1, x0] * (1 - fx) * fy + img[y1, x1] * fx * fy)
bestd = np.full(len(tid), 9.0); which = np.zeros(len(tid), np.int8); offs = np.cumsum([0] + [len(x['V']) for x in G]); has_img = np.array([x['img'] is not None for x in G])
for ki, key in enumerate(('flat2', 'flat1')):
    Vp, Fp_all, gid_all, _ = placed(key); keepf = has_img[gid_all]; Fp = Fp_all[keepf]; gid = gid_all[keepf]            # only textured GLB faces can be hit
    mesh = trimesh.Trimesh(Vp, Fp, process=False); ray = RayMeshIntersector(mesh); fn = mesh.face_normals; p0 = Vp[Fp[:, 0]]
    O = X + 0.5 * N; D = -N; tri = ray.intersects_first(O, D); hit = tri >= 0; t = np.full(len(X), 9.0)
    t[hit] = np.einsum('ij,ij->i', p0[tri[hit]] - O[hit], fn[tri[hit]]) / np.einsum('ij,ij->i', D[hit], fn[tri[hit]])
    align = np.zeros(len(X)); align[hit] = np.abs(np.einsum('ij,ij->i', D[hit], fn[tri[hit]])); g_of = np.where(hit, gid[np.maximum(tri, 0)], -1)
    ok = hit & (t > 0.05) & (t < 1.0) & (align > 0.7) & (np.abs(t - 0.5) < bestd) & (g_of >= 0) & has_img[np.maximum(g_of, 0)]; idx = np.where(ok)[0]; log(key, "GLB hits accepted", len(idx))
    P = O[idx] + D[idx] * t[idx, None]; bc = trimesh.triangles.points_to_barycentric(Vp[Fp[tri[idx]]], P)
    for gi in np.unique(g_of[idx]):
        m_ = g_of[idx] == gi; sl = idx[m_]; local = Fp[tri[sl]] - offs[gi]; uv = (bc[m_][:, :, None] * G[gi]['uv'][local]).sum(1)
        colors[sl] = bilinear(G[gi]['img'], uv[:, 0], uv[:, 1]); which[sl] = ki + 1; bestd[sl] = np.abs(t[sl] - 0.5); src_kind[sl] = 2
log("GLB texels: flat2 %d  flat1 %d | drone-only %d | none %d" % ((which == 1).sum(), (which == 2).sum(), (src_kind == 1).sum(), (src_kind == 0).sum()))
per = {}
for k, (o, v, n, uv, size) in enumerate(isl):
    m_ = TOk[tid] == k; per[o.name] = {'n': int(m_.sum()), 'none': int((src_kind[m_] == 0).sum()), 'drone': int((src_kind[m_] == 1).sum()), 'glb': int((src_kind[m_] == 2).sum()), 'normal': [round(float(x), 2) for x in n], 'centroid': [round(float(x), 2) for x in v.mean(0)], 'area_m2': round(float((size[0] * size[1])), 2)}
json.dump(per, open(os.path.join(out, 'per_object.json'), 'w'), indent=0)
NAf = NA.reshape(-1, 3); NAf[tex] = np.clip(colors, 0, 255).astype(np.uint8)
kind = np.zeros(ATL * ATL, np.uint8); kind[tex] = src_kind + 1; kind = kind.reshape(ATL, ATL)             # 1 none, 2 drone, 3 GLB
valid = (kind > 0).astype(np.uint8); dist, lab = cv2.distanceTransformWithLabels((1 - valid).astype(np.uint8), cv2.DIST_L2, 3, labelType=cv2.DIST_LABEL_PIXEL)
ys_, xs_ = np.where(valid > 0); lm = np.zeros(lab.max() + 1, np.int64); lm[lab[ys_, xs_]] = ys_ * ATL + xs_; near = lm[lab]; gut = (dist <= 4) & (valid == 0)
NA = NAf.reshape(ATL, ATL, 3); NA[gut] = NA.reshape(-1, 3)[near[gut]]
cv2.imwrite(os.path.join(out, 'flat_glb_atlas.png'), NA); cv2.imwrite(os.path.join(out, 'flat_glb_coverage.png'), np.select([kind == 3, kind == 2, kind == 1], [255, 160, 60], 0).astype(np.uint8))
json.dump({'texel_cm': ts * 100, 'objects': len(sel), 'texels': int(len(tid)), 'glb_flat2': int((which == 1).sum()), 'glb_flat1': int((which == 2).sum()), 'drone_only': int((src_kind == 1).sum()), 'none': int((src_kind == 0).sum())}, open(os.path.join(out, 'transfer_stats.json'), 'w'), indent=1)
# ---------- blend: new UV layer + material for the moved objects ----------
nimg = bpy.data.images.load(os.path.abspath(os.path.join(out, 'flat_glb_atlas.png'))); nimg.name = 'Flat_GLB_atlas'; nimg.pack()
mat = bpy.data.materials.new('Flat_GLB_AtlasMaterial'); mat.use_nodes = True; nt = mat.node_tree; b = nt.nodes['Principled BSDF']; b.inputs['Roughness'].default_value = 1.0
ti = nt.nodes.new('ShaderNodeTexImage'); ti.image = nimg; uvn = nt.nodes.new('ShaderNodeUVMap'); uvn.uv_map = 'GLB_UV'; nt.links.new(uvn.outputs['UV'], ti.inputs['Vector']); nt.links.new(ti.outputs['Color'], b.inputs['Base Color'])
for k, (o, v, n, uv, size) in enumerate(isl):
    me = o.data; pix = np.array(o['glb_uv']); layer = me.uv_layers.get('GLB_UV') or me.uv_layers.new(name='GLB_UV')
    for l in me.loops: layer.data[l.index].uv = (pix[l.vertex_index][0] / ATL, 1 - pix[l.vertex_index][1] / ATL)
    me.uv_layers.active = layer; layer.active_render = True; me.materials.clear(); me.materials.append(mat); del o['glb_uv']
# ---------- floors from the GLB ----------
coll = {k: bpy.data.collections.new(n) for k, n in (('flat2', 'Flat2_GLB_floors'), ('flat1', 'Flat1_GLB_floors_mirrored'))}
for c in coll.values(): bpy.context.scene.collection.children.link(c)
mats = {}
def make_mat(gi):
    x = G[gi]; nm = x['mat']
    if nm in mats: return mats[nm]
    m = bpy.data.materials.new(nm); m.use_nodes = True; bb = m.node_tree.nodes['Principled BSDF']; bb.inputs['Roughness'].default_value = 0.9
    if x['img'] is not None:
        p = os.path.abspath(os.path.join(out, 'floor_tex_%s.png' % nm)); cv2.imwrite(p, x['img']); t = m.node_tree.nodes.new('ShaderNodeTexImage'); t.image = bpy.data.images.load(p); t.image.pack(); m.node_tree.links.new(t.outputs['Color'], bb.inputs['Base Color'])
    else: bb.inputs['Base Color'].default_value = (0.62, 0.6, 0.57, 1)
    mats[nm] = m; return m
import bmesh
nfloor = 0
for key in ('flat2', 'flat1'):
    p = PL[key]; sh = np.array(p['shift']); mir = p['mirror']
    for gi, x in enumerate(G):
        if not x['floor']: continue
        V = x['V'].copy(); F = x['F'].copy()
        if mir: V[:, 1] = -V[:, 1]
        V = V + sh; me = bpy.data.meshes.new(('Flat2_' if key == 'flat2' else 'Flat1m_') + x['mat'].replace('MAT_F2_', '')); me.from_pydata(V.tolist(), [], F.tolist()); me.update()
        me.uv_layers.new(name='GLB_UV'); ud = me.uv_layers[0].data
        for poly in me.polygons:
            for li, vi in zip(poly.loop_indices, poly.vertices): ud[li].uv = tuple(x['uv'][vi])
        me.materials.append(make_mat(gi)); o = bpy.data.objects.new(me.name, me); coll[key].objects.link(o); nfloor += 1
        bm = bmesh.new(); bm.from_mesh(me); up = np.mean([f.normal.z for f in bm.faces]) if bm.faces else 1
        if up < 0: bmesh.ops.reverse_faces(bm, faces=bm.faces[:]); bm.to_mesh(me)
        bm.free()
log("floor objects added", nfloor)
bpy.ops.wm.save_as_mainfile(filepath=os.path.join(out, 'Floor18_textured_flat2_glb.blend')); log("saved")
