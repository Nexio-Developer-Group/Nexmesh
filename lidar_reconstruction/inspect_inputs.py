"""Phase 1: inventory a .blend scene (objects, transforms, units, floor elevations) and print JSON."""
import sys, json
import bpy
import mathutils


def inspect(path):
    bpy.ops.wm.open_mainfile(filepath=path)
    sc = bpy.context.scene
    out = {"blender": bpy.app.version_string, "scene": sc.name,
           "unit_system": sc.unit_settings.system, "scale_length": sc.unit_settings.scale_length,
           "length_unit": sc.unit_settings.length_unit, "collections": {}, "objects": []}
    for c in bpy.data.collections:
        out["collections"][c.name] = [o.name for o in c.objects]
    for o in bpy.data.objects:
        d = {"name": o.name, "type": o.type, "parent": o.parent.name if o.parent else None,
             "loc": list(o.location), "rot_euler": list(o.rotation_euler), "scale": list(o.scale),
             "collections": [c.name for c in o.users_collection], "hide": o.hide_get() if o.name in bpy.context.view_layer.objects else None}
        mw = o.matrix_world
        d["world_loc"] = list(mw.translation)
        if o.type == "MESH":
            me = o.data
            ws = [mw @ v.co for v in me.vertices]
            if ws:
                mn = [min(p[i] for p in ws) for i in range(3)]; mx = [max(p[i] for p in ws) for i in range(3)]
                d.update(verts=len(me.vertices), faces=len(me.polygons), world_bbox_min=mn, world_bbox_max=mx,
                         mesh=me.name, modifiers=[m.type for m in o.modifiers])
        d["custom_props"] = {k: str(o[k]) for k in o.keys() if not k.startswith("_")}
        out["objects"].append(d)
    return out


if __name__ == "__main__":
    res = inspect(sys.argv[1])
    if len(sys.argv) > 2:  # Blender logs to stdout, so write JSON to a file
        json.dump(res, open(sys.argv[2], "w"), indent=1, default=str)
    else:
        print(json.dumps(res, indent=1, default=str))
