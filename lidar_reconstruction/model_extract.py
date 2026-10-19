"""Slice the Blender model at the LiDAR height and export 2D section segments (world metres) per object kind.
Original .blend is only read, never saved."""
import sys, json
import numpy as np
import bpy


def slice_segments(blend, z, out_json, skip=("Sphere",)):  # z: slice height in world metres
    bpy.ops.wm.open_mainfile(filepath=blend)
    segs = []
    for o in bpy.data.objects:
        if o.type != "MESH" or o.name in skip:
            continue
        kind = o.name.split("_")[0]
        coll = o.users_collection[0].name
        mw = o.matrix_world
        V = np.array([tuple(mw @ v.co) for v in o.data.vertices])
        if len(V) == 0 or V[:, 2].min() > z or V[:, 2].max() < z:
            continue
        me = o.data; me.calc_loop_triangles()
        edges = {}
        for t in me.loop_triangles:
            P = V[list(t.vertices)]
            s = P[:, 2] - z
            pts = []
            for a, b in ((0, 1), (1, 2), (2, 0)):
                if (s[a] > 0) != (s[b] > 0):
                    f = s[a] / (s[a] - s[b]); pts.append(P[a] + f * (P[b] - P[a]))
            if len(pts) == 2 and np.linalg.norm(pts[0][:2] - pts[1][:2]) > 1e-6:
                key = tuple(sorted([tuple(np.round(pts[0][:2], 5)), tuple(np.round(pts[1][:2], 5))]))
                edges[key] = edges.get(key, 0) + 1
        for (a, b), c in edges.items():
            segs.append(dict(kind=kind, coll=coll, name=o.name, p=[a[0], a[1], b[0], b[1]]))
    json.dump(segs, open(out_json, "w"))
    return segs


def extract_text(blend, out_json):
    """Room labels, nominal room sizes (e.g. '3200X4025'), door/window tags and dimension annotations."""
    bpy.ops.wm.open_mainfile(filepath=blend)
    out = [dict(name=o.name, coll=o.users_collection[0].name, body=o.data.body, xy=list(o.matrix_world.translation)[:2], rot_z=o.rotation_euler[2])
           for o in bpy.data.objects if o.type == "FONT"]
    json.dump(out, open(out_json, "w")); return out


if __name__ == "__main__":
    slice_segments(sys.argv[1], float(sys.argv[2]), sys.argv[3])
