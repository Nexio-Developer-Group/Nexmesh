"""usage: render_views.py <blend or glb> out_prefix  (renders 3 views inside flat2 and the same 3 mirrored in flat1)"""
import bpy, sys, math, json, numpy as np
from mathutils import Vector, Matrix
src, outp = sys.argv[1], sys.argv[2]
if src.endswith('.glb'):
    bpy.ops.wm.read_factory_settings(use_empty=True); bpy.ops.import_scene.gltf(filepath=src)
    PL = json.load(open('placement.json'))
    objs = [o for o in bpy.data.objects if o.type == 'MESH']; roots = [o for o in bpy.data.objects if o.parent is None]
    # place a copy per flat: flat2 as translated, flat1 as mirrored
    import copy
    originals = list(objs)
    for o in originals:
        o.matrix_world = Matrix.Translation(PL['flat2']['shift']) @ o.matrix_world
    for o in originals:
        d = o.copy(); d.data = o.data.copy(); bpy.context.scene.collection.objects.link(d)
        M = Matrix.Translation(PL['flat1']['shift']) @ Matrix.Diagonal((1, -1, 1, 1)) @ Matrix.Translation([-x for x in PL['flat2']['shift']]) @ o.matrix_world
        d.matrix_world = M
        import bmesh; bm = bmesh.new(); bm.from_mesh(d.data); bm.transform(Matrix.Diagonal((1, 1, 1, 1))); bmesh.ops.reverse_faces(bm, faces=bm.faces[:]); bm.to_mesh(d.data); bm.free()
else:
    bpy.ops.wm.open_mainfile(filepath=src)
s = bpy.context.scene; s.render.engine = 'BLENDER_WORKBENCH'; s.render.resolution_x, s.render.resolution_y, s.render.resolution_percentage = 1280, 720, 50
s.display.shading.light = 'FLAT'; s.display.shading.color_type = 'TEXTURE'; s.display.shading.show_backface_culling = False
cam = bpy.data.cameras.new('c'); cam.lens = 20.4; cam.sensor_width = 36; co = bpy.data.objects.new('c', cam); s.collection.objects.link(co); s.camera = co
def look(loc, yaw_deg, pitch=0):
    y, p = math.radians(yaw_deg), math.radians(pitch); f = Vector((math.cos(p) * math.cos(y), math.cos(p) * math.sin(y), math.sin(p))); r = f.cross(Vector((0, 0, 1))).normalized(); u = r.cross(f)
    M = Matrix(((r.x, u.x, -f.x, loc[0]), (r.y, u.y, -f.y, loc[1]), (r.z, u.z, -f.z, loc[2]), (0, 0, 0, 1))); return M
# flat 2 views (glb placed): camera in the large living/dining area & bedrooms; mirrored positions for flat 1
views = [((-13.5, 5.5, 1.5), 90), ((-13.5, 5.5, 1.5), 180), ((-13.5, 5.5, 1.5), 0), ((-16.0, 2.0, 1.5), 90), ((-12.0, 8.0, 1.5), 270)]
for k, (loc, yaw) in enumerate(views):
    for flat in ('2', '1'):
        if flat == '1': loc2 = (loc[0], -2 * 1.625 - loc[1] + 0.0 + 0.0, loc[2]); yaw2 = -yaw
        else: loc2, yaw2 = loc, yaw
        co.matrix_world = look(loc2, yaw2); s.render.filepath = f'{outp}_f{flat}_v{k}.png'; bpy.ops.render.render(write_still=True)
