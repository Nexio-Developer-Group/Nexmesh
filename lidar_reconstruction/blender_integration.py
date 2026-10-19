"""Build output/registered_scene.blend from a COPY of the original model: original building, sphere, registered point cloud,
trajectory, pose markers, floor-plan geometry, registration references and diagnostics, in separate collections.
The original .blend is only read. Original object transforms are not modified."""
import json
import numpy as np
import bpy
import build_map as BM
from geometry import voxel_filter


def new_coll(name, parent=None, hide=False):
    c = bpy.data.collections.new(name)
    (parent or bpy.context.scene.collection).children.link(c)
    if hide:
        bpy.context.view_layer.layer_collection.children[name].hide_viewport = True
    return c


def mesh_obj(name, verts, edges=(), faces=(), coll=None, z=0.0, attrs=None):
    me = bpy.data.meshes.new(name + "_mesh")
    v = np.asarray(verts, float)
    if v.shape[1] == 2:
        v = np.c_[v, np.full(len(v), z)]
    me.from_pydata(v.tolist(), list(edges), list(faces)); me.update()
    for k, (arr, typ) in (attrs or {}).items():
        a = me.attributes.new(k, typ, "POINT"); a.data.foreach_set("value", np.asarray(arr, np.float32 if typ == "FLOAT" else np.int32))
    o = bpy.data.objects.new(name, me); coll.objects.link(o); return o


def material(name, rgba):
    m = bpy.data.materials.get(name) or bpy.data.materials.new(name)
    m.diffuse_color = rgba; return m


def points_modifier(obj, radius):
    """Geometry-nodes modifier that renders a vertex-only mesh as points (radius is exposed on the modifier)."""
    ng = bpy.data.node_groups.new("PointCloudDisplay_" + obj.name, "GeometryNodeTree")
    ng.interface.new_socket("Geometry", in_out="INPUT", socket_type="NodeSocketGeometry")
    ng.interface.new_socket("Geometry", in_out="OUTPUT", socket_type="NodeSocketGeometry")
    s = ng.interface.new_socket("Point radius (m)", in_out="INPUT", socket_type="NodeSocketFloat"); s.default_value = radius; s.min_value = 0.0005
    gi = ng.nodes.new("NodeGroupInput"); go = ng.nodes.new("NodeGroupOutput"); mp = ng.nodes.new("GeometryNodeMeshToPoints")
    ng.links.new(gi.outputs[0], mp.inputs["Mesh"]); ng.links.new(gi.outputs[1], mp.inputs["Radius"]); ng.links.new(mp.outputs["Points"], go.inputs[0])
    md = obj.modifiers.new("PointCloudDisplay", "NODES"); md.node_group = ng
    return md


def run(cfg, P):
    inv = json.load(open(P(cfg, "inventory.json"))); reg = json.load(open(P(cfg, "registration.json"))); g = json.load(open(P(cfg, "floorplan_geometry.json")))
    an = np.load(P(cfg, "anchored.npz")); sp = np.load(P(cfg, "scan_poses.npz")); so = np.load(P(cfg, "slam_only.npz"))
    zs = inv["sphere"]["world_loc"][2]
    bpy.ops.wm.open_mainfile(filepath=cfg["paths"]["blend"])     # read original; saved elsewhere below
    sc = bpy.context.scene
    # --- ORIGINAL_BUILDING: re-parent existing collections (object transforms untouched)
    orig = new_coll("ORIGINAL_BUILDING")
    for c in list(sc.collection.children):
        if c.name != orig.name:
            sc.collection.children.unlink(c); orig.children.link(c)
    sphere = bpy.data.objects[inv["sphere"]["name"]]
    start = new_coll("START_REFERENCE")
    for c in sphere.users_collection:
        c.objects.unlink(sphere)
    start.objects.link(sphere)
    sphere["role"] = "start reference (given by user); world xyz kept unchanged"
    sphere["registration_offset_to_first_pose_m"] = reg["first_pose_after_anchoring"]["offset_from_sphere_m"]
    for o in list(sc.collection.objects):
        if o.type == "LIGHT":
            continue
    # --- registered point cloud (display subsample, full resolution kept in PLY)
    reg_c = new_coll("LIDAR_REGISTERED")
    ply = BM.read_ply(P(cfg, "registered_processed.ply"))
    xyz = np.c_[ply["x"], ply["y"], ply["z"]].astype(float)
    from scipy.spatial import cKDTree
    disp = voxel_filter(xyz[:, :2], 0.05)
    keep = cKDTree(xyz[:, :2]).query(disp)[1]
    o = mesh_obj("LiDAR_Registered_Points", xyz[keep], coll=reg_c,
                 attrs={"t_s": (ply["t"][keep], "FLOAT"), "scan_id": (ply["scan_id"][keep], "INT")})
    o["source_ply"] = "output/registered_processed.ply (full 2 cm cloud)"; o["frame"] = "Blender world, metres"
    points_modifier(o, 0.012)
    # --- LIDAR_RAW: LiDAR-only SLAM-frame cloud (unregistered, hidden by default)
    raw_c = new_coll("LIDAR_RAW", hide=True)
    rp = BM.read_ply(P(cfg, "slam_only_unregistered.ply"))
    rv = voxel_filter(np.c_[rp["x"], rp["y"]], 0.06)
    o = mesh_obj("LiDAR_SLAMonly_Points_SLAMframe", np.c_[rv, np.zeros(len(rv))], coll=raw_c); points_modifier(o, 0.012)
    o["note"] = "LiDAR-only SLAM frame (first pose = origin, yaw arbitrary); NOT in Blender world coordinates"
    # --- TRAJECTORY
    tr = new_coll("TRAJECTORY")
    poses = sp["poses"]; t = np.load(P(cfg, "lidar_cache.npz"))["t0"]
    step = 5
    pts = np.c_[poses[::step, :2], np.full(len(poses[::step]), zs)]
    cu = bpy.data.curves.new("Trajectory_curve", "CURVE"); cu.dimensions = "3D"; cu.bevel_depth = 0.02; cu.resolution_u = 1
    spl = cu.splines.new("POLY"); spl.points.add(len(pts) - 1)
    for i, p in enumerate(pts):
        spl.points[i].co = (*p, 1.0)
    co = bpy.data.objects.new("Trajectory", cu); tr.objects.link(co); co["points"] = len(pts); co["z_assumed_note"] = "z = sphere height; vertical motion is not observable with a horizontal 2D LiDAR"
    co.data.materials.append(material("traj_mat", (0.1, 0.4, 1.0, 1)))
    tsel = np.arange(0, len(poses), int(60 * 13.33))      # one pose marker per minute
    for i in tsel:
        e = bpy.data.objects.new("pose_t%04ds_scan%05d" % (int(t[i] - t[0]), i), None); e.empty_display_type = "SINGLE_ARROW"; e.empty_display_size = 0.6
        e.location = (poses[i, 0], poses[i, 1], zs); e.rotation_euler = (0, np.pi / 2, poses[i, 2]); e.rotation_mode = "XYZ"
        e["t_s"] = float(t[i] - t[0]); e["scan_id"] = int(i); tr.objects.link(e)
    kf = an["kf_pose"]
    o = mesh_obj("Keyframes", np.c_[kf[:, :2], np.full(len(kf), zs)], coll=tr, attrs={"sigma_xy_m": (an["sigma_xy"], "FLOAT"), "anchor_inlier": (an["anchor_inlier"], "FLOAT")}); points_modifier(o, 0.03)
    # --- FLOOR_PLAN_GEOMETRY (measured vs model-derived in separate objects/materials)
    fpc = new_coll("FLOOR_PLAN_GEOMETRY"); zf = 0.03
    def edges_obj(name, segs, rgba, coll):
        V, E = [], []
        for a, b in segs:
            E.append((len(V), len(V) + 1)); V += [a, b]
        o = mesh_obj(name, V or [[0, 0]], E, coll=coll, z=zf); o.data.materials.append(material(name + "_mat", rgba)); return o
    edges_obj("Walls_LiDAR_observed", [(f["p0"], f["p1"]) for f in g["faces"]], (0, 0, 0, 1), fpc)
    edges_obj("Walls_model_derived", [(m["p0"], m["p1"]) for m in g["model_derived_unobserved"]], (0.55, 0.55, 0.55, 1), fpc)
    edges_obj("Openings_observed", [(o_["p0"], o_["p1"]) for o_ in g["openings"]], (0.9, 0.5, 0, 1), fpc)
    rc = {"observed": (0.55, 0.75, 0.95, 1), "partially_observed": (0.99, 0.94, 0.7, 1), "model_derived": (0.8, 0.8, 0.8, 1)}
    for r in g["rooms"]:
        V = [list(p) for p in r["polygon"][:-1]]
        o = mesh_obj("Room_%s_%s" % (r["id"], r["status"]), V, faces=[tuple(range(len(V)))], coll=fpc, z=0.01)
        o.data.materials.append(material("room_" + r["status"], rc[r["status"]]))
        o["label"] = r["label"] or ""; o["area_m2"] = r["area_m2"]; o["min_rect_m"] = r["min_rect_m"]; o["observed_perimeter_frac"] = r["observed_perimeter_frac"]; o["status"] = r["status"]
    # --- REGISTRATION_REFERENCES
    rf = new_coll("REGISTRATION_REFERENCES")
    e = bpy.data.objects.new("Estimated_start_pose", None); e.empty_display_type = "SINGLE_ARROW"; e.empty_display_size = 1.0
    e.location = (kf[0, 0], kf[0, 1], zs); e.rotation_mode = "XYZ"; e.rotation_euler = (0, np.pi / 2, kf[0, 2]); rf.objects.link(e)
    e["offset_from_sphere_m"] = reg["first_pose_after_anchoring"]["offset_from_sphere_m"]
    anc = an["anchor_chunks"]
    mesh_obj("Anchor_chunk_centres", np.c_[kf[anc, :2], np.full(len(anc), zs)], coll=rf)
    reg_text = bpy.data.texts.new("registration_report.json"); reg_text.write(json.dumps(reg, indent=1))
    # --- DIAGNOSTICS: loop-closure edges (LiDAR-only loops drawn between the anchored keyframes), residual per keyframe
    dg = new_coll("DIAGNOSTICS")
    loops = so["loops"]; V, E = [], []
    for l in loops:
        i, j = int(l[0]), int(l[1]); E.append((len(V), len(V) + 1)); V += [[kf[i, 0], kf[i, 1], zs], [kf[j, 0], kf[j, 1], zs]]
    if V:
        mesh_obj("Loop_closures", V, E, coll=dg)
    mesh_obj("Anchor_residual_rms", np.c_[kf[:, :2], np.full(len(kf), zs + 0.1)], coll=dg, attrs={"rms_m": (np.nan_to_num(an["anchor_rms"]), "FLOAT")})
    sc.unit_settings.system = "METRIC"
    bpy.ops.wm.save_as_mainfile(filepath=P(cfg, "registered_scene.blend"), compress=True)
