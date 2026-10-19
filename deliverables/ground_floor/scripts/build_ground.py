import bpy, sys, json, numpy as np, cv2, trimesh, bmesh, os
from shapely.geometry import Polygon
from shapely.ops import unary_union
src, tex, stj, fpn, out = sys.argv[1:6]; os.makedirs(out, exist_ok=True); st = json.load(open(stj)); fp = np.load(fpn); RES = 0.05; lo = np.array([-21., -14.])
cnts, hier = cv2.findContours(fp, cv2.RETR_CCOMP, cv2.CHAIN_APPROX_SIMPLE); w2 = lambda c: [(lo[0] + (p[0][0] + .5) * RES, lo[1] + (p[0][1] + .5) * RES) for p in c]; polys = []
for i, c in enumerate(cnts):
    if hier[0][i][3] == -1 and len(c) >= 4:
        holes = [w2(cnts[j]) for j in range(len(cnts)) if hier[0][j][3] == i and len(cnts[j]) >= 4]; pg = Polygon(w2(c), holes).buffer(0)
        if pg.area > 1: polys.append(pg.simplify(0.03))
mp = unary_union(polys); geoms = list(mp.geoms) if hasattr(mp, 'geoms') else [mp]; Vs = []; Fs = []; off = 0
for g in geoms:
    v2, f = trimesh.creation.triangulate_polygon(g, engine='earcut'); Vs.append(np.c_[v2, np.zeros(len(v2))]); Fs.append(f + off); off += len(v2)
V = np.vstack(Vs); F = np.vstack(Fs); print("ground mesh: polygons", len(geoms), "verts", len(V), "tris", len(F), "area %.0f m2" % mp.area)
W, H, ts, x0, y1 = st['grid_W'], st['grid_H'], st['texel_cm'] / 100, st['x0'], st['y1']; ybot = y1 - H * ts
bpy.ops.wm.open_mainfile(filepath=src)
me = bpy.data.meshes.new('Floor_Drone_Ground'); me.from_pydata(V.tolist(), [], F.tolist()); me.update(); uv = me.uv_layers.new(name='GroundUV')
for poly in me.polygons:
    for li, vi in zip(poly.loop_indices, poly.vertices): uv.data[li].uv = ((V[vi][0] - x0) / (W * ts), (V[vi][1] - ybot) / (H * ts))
bm = bmesh.new(); bm.from_mesh(me); up = np.mean([f.normal.z for f in bm.faces])
if up < 0: bmesh.ops.reverse_faces(bm, faces=bm.faces[:])
bm.to_mesh(me); bm.free()
img = bpy.data.images.load(os.path.abspath(tex)); img.name = 'Floor_drone_ground'; img.pack()
mat = bpy.data.materials.new('Floor18_DroneGround'); mat.use_nodes = True; nt = mat.node_tree; b = nt.nodes['Principled BSDF']; b.inputs['Roughness'].default_value = 1.0
ti = nt.nodes.new('ShaderNodeTexImage'); ti.image = img; uvn = nt.nodes.new('ShaderNodeUVMap'); uvn.uv_map = 'GroundUV'; nt.links.new(uvn.outputs['UV'], ti.inputs['Vector']); nt.links.new(ti.outputs['Color'], b.inputs['Base Color'])
me.materials.append(mat); ob = bpy.data.objects.new('Floor_Drone_Ground', me); bpy.data.collections['01_Slab'].objects.link(ob)
# GLB floors: drop the untextured grey placeholders, lower the textured ones to the same floor level
removed = []
for o in list(bpy.data.objects):
    if o.name.startswith(('Flat2_', 'Flat1m_')) and o.type == 'MESH':
        m = o.data.materials[0] if o.data.materials else None; has_tex = bool(m and any(n.type == 'TEX_IMAGE' for n in m.node_tree.nodes))
        if not has_tex: removed.append(o.name); bpy.data.objects.remove(o, do_unlink=True)
        else: o.location.z -= 0.12 - 0.004
print("removed untextured GLB floor placeholders:", len(removed), removed[:3]); 
bpy.ops.wm.save_as_mainfile(filepath=os.path.join(out, 'Floor18_textured_Flat1_Flat2_glb_ground.blend')); print("saved")
