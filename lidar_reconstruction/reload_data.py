"""Reload the externally stored full-resolution data into any open Blender session (Scripting tab -> Open -> Run), or from the CLI:
    python reload_data.py output/registered_scene.blend          # adds the full 2 cm cloud to the saved scene
The scene itself only holds a 5 cm display subsample; the full cloud is output/registered_processed.ply (x,y,z, scan_id, t)."""
import os, sys
import bpy

HERE = os.path.dirname(os.path.abspath(__file__)) if "__file__" in globals() else os.getcwd()


def load_full_cloud(ply_path=None, collection="LIDAR_REGISTERED", name="LiDAR_Registered_FULL_2cm"):
    ply_path = ply_path or os.path.join(HERE, "output", "registered_processed.ply")
    before = set(bpy.data.objects)
    bpy.ops.wm.ply_import(filepath=ply_path)
    obj = [o for o in bpy.data.objects if o not in before][0]
    obj.name = name
    for c in obj.users_collection:
        c.objects.unlink(obj)
    bpy.data.collections[collection].objects.link(obj)
    return obj


if __name__ == "__main__":
    if len(sys.argv) > 1:
        bpy.ops.wm.open_mainfile(filepath=sys.argv[1])
        load_full_cloud(); bpy.ops.wm.save_as_mainfile(filepath=sys.argv[1].replace(".blend", "_full.blend"))
    else:
        load_full_cloud()
