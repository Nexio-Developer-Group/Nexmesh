"""Build textured .blend + camera animation + pose JSON/CSV. usage: bpy 09_build_blend.py src.blend atlas_uv.npz texture.png poses.json out_dir"""
import bpy, sys, json, re, csv, os, numpy as np
from mathutils import Matrix
src, uvnpz, tex, posef, out = sys.argv[1:6]; os.makedirs(out, exist_ok=True)
bpy.ops.wm.open_mainfile(filepath=src)
d = np.load(uvnpz); UV = d['UV']; ATL = int(d['ATL']); k = 0
for o in (o for o in bpy.data.objects if o.type == 'MESH'):
    me = o.data; me.calc_loop_triangles()
    uvl = me.uv_layers.get('BakedUV') or me.uv_layers.new(name='BakedUV')
    for t in me.loop_triangles:
        for l, c in zip(t.loops, UV[k]): uvl.data[l].uv = (c[0] / ATL, 1 - c[1] / ATL)
        k += 1
    me.uv_layers.active = uvl; uvl.active_render = True
img = bpy.data.images.load(os.path.abspath(tex)); img.name = 'Floor18_baked'; img.pack()
mat = bpy.data.materials.new('Floor18_BakedTexture'); mat.use_nodes = True; nt = mat.node_tree
bsdf = nt.nodes['Principled BSDF']; ti = nt.nodes.new('ShaderNodeTexImage'); ti.image = img; ti.interpolation = 'Linear'
nt.links.new(ti.outputs['Color'], bsdf.inputs['Base Color']); bsdf.inputs['Roughness'].default_value = 1.0
uvn = nt.nodes.new('ShaderNodeUVMap'); uvn.uv_map = 'BakedUV'; nt.links.new(uvn.outputs['UV'], ti.inputs['Vector'])
for o in bpy.data.objects:
    if o.type == 'MESH': o.data.materials.clear(); o.data.materials.append(mat)
P = json.load(open(posef)); FLIP = np.diag([1., -1., -1., 1.])
def tsec(n):
    m = re.findall(r'\d+', n); return (int(m[0]) - 1) / 3 if len(m) == 1 else 295 + int(m[1]) * 0.1
names = sorted(P, key=tsec); q = P[names[0]]['params']; W, H = P[names[0]]['W'], P[names[0]]['H']
cam = bpy.data.cameras.new('DroneCam'); cam.sensor_width = 36; cam.sensor_fit = 'HORIZONTAL'; cam.lens = q[0] / W * 36; cam.clip_start = 0.05
co = bpy.data.objects.new('DroneCam_estimated', cam); bpy.context.scene.collection.objects.link(co)
bpy.context.scene.render.resolution_x, bpy.context.scene.render.resolution_y = W, H; bpy.context.scene.render.fps = 30
rows = []; out_poses = {}
for i, n in enumerate(names):
    Rc, C = np.array(P[n]['R_c2w_unrefined']), np.array(P[n]['C_unrefined']); M = np.eye(4); M[:3, :3] = Rc; M[:3, 3] = C; M = M @ FLIP   # OpenCV cam -> Blender cam
    co.matrix_world = Matrix(M.tolist()); co.keyframe_insert('location', frame=i + 1); co.keyframe_insert('rotation_euler', frame=i + 1)
    out_poses[n] = {'time_s': tsec(n), 'blender_matrix_world': M.tolist(), 'location': M[:3, 3].tolist(), 'rotation_euler_xyz_deg': np.degrees(np.array(Matrix(M.tolist()).to_euler())).tolist(),
                    'opencv_K_at_1920x1080': [[q[0], 0, P[n]['params'][2]], [0, P[n]['params'][1], P[n]['params'][3]], [0, 0, 1]], 'opencv_dist_k1k2p1p2': P[n]['params'][4:8]}
    rows.append([n, round(tsec(n), 3)] + [round(x, 5) for x in M[:3, 3]] + [round(x, 4) for x in out_poses[n]['rotation_euler_xyz_deg']])
bpy.context.scene.frame_end = len(names)
json.dump({'camera_model': 'OPENCV (fx,fy,cx,cy,k1,k2,p1,p2) shared, at 1920x1080', 'intrinsics_params': q, 'units': 'metres (Blender world)', 'frames': out_poses}, open(os.path.join(out, 'camera_poses_blender.json'), 'w'), indent=1)
with open(os.path.join(out, 'camera_poses_blender.csv'), 'w', newline='') as f:
    w = csv.writer(f); w.writerow(['image', 'time_s', 'x', 'y', 'z', 'rot_x_deg', 'rot_y_deg', 'rot_z_deg']); w.writerows(rows)
bpy.ops.wm.save_as_mainfile(filepath=os.path.join(out, 'Floor18_textured.blend')); print("saved", len(names), "cams; lens", cam.lens)
