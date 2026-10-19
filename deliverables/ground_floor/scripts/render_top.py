import bpy, sys
bpy.ops.wm.open_mainfile(filepath=sys.argv[1]); s = bpy.context.scene; s.render.engine = 'BLENDER_WORKBENCH'; s.render.resolution_x, s.render.resolution_y, s.render.resolution_percentage = 1600, 1200, 100
s.display.shading.light = 'FLAT'; s.display.shading.color_type = 'TEXTURE'; s.display.shading.show_backface_culling = False
for o in bpy.data.objects:
    if o.type == 'MESH' and o.name != 'Floor_Drone_Ground' and not o.name.startswith(('Flat2_', 'Flat1m_')): o.hide_render = True        # hide walls: show floors only
cam = bpy.data.cameras.new('t'); cam.type = 'ORTHO'; cam.ortho_scale = 40; co = bpy.data.objects.new('t', cam); s.collection.objects.link(co); s.camera = co; co.location = (-1.5, 0.3, 30); co.rotation_euler = (0, 0, 0)
s.render.filepath = sys.argv[2]; bpy.ops.render.render(write_still=True)
