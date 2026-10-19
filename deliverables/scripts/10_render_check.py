import bpy, sys
bpy.ops.wm.open_mainfile(filepath=sys.argv[1]); s = bpy.context.scene
s.render.engine = 'BLENDER_WORKBENCH'; s.render.resolution_x, s.render.resolution_y, s.render.resolution_percentage = 1920, 1080, 50
s.display.shading.light = 'FLAT'; s.display.shading.color_type = 'TEXTURE'; s.camera = bpy.data.objects['DroneCam_estimated']
for f in map(int, sys.argv[3].split(',')):
    s.frame_set(f); s.render.filepath = f"{sys.argv[2]}/render_{f:04d}.png"; bpy.ops.render.render(write_still=True)
