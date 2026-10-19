"""SIMULATED 3D cloud built ONLY from the simulated 2D cloud (SIMULATED_registered_direct_hits.ply). Illustrative, not measured.
- points within 6 cm of a (simulated) wall face are extended vertically from floor (z=0) to roof (model height) in 0.1 m steps
  -> assumes vertical walls; flagged is_measured=0
- all other points (furniture/clutter/glass returns) stay at their real sensor height only; flagged is_measured=1 for the sensor-height wall layer too
- every z gets an assumed multiplicative error of +-0.8 % (uniform), z' = z * (1 + 0.008 u), u ~ U(-1, 1)"""
import json, os, sys
import numpy as np
from scipy.spatial import cKDTree
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_map as BM
from geometry import voxel_filter


def run(root, out, zstep=0.10, xy_vox=0.06, zerr=0.008, seed=3):
    O = os.path.join(root, "output"); S = out
    ply = BM.read_ply(f"{S}/SIMULATED_registered_direct_hits.ply"); xy = np.c_[ply["x"], ply["y"]].astype(float); zs = float(ply["z"][0]); sid = ply["scan_id"]
    g = json.load(open(f"{O}/floorplan_geometry.json")); fs = json.load(open(f"{S}/SIMULATED_face_shifts_m.json"))
    sl = [s for s in json.load(open(f"{O}/model_slice.json"))]  # unused here; roof height from the Blender file
    import bpy
    bpy.ops.wm.open_mainfile(filepath=os.path.join(root, "data", "Floor18_TypicalFloor_WingB.blend"))
    ztop = max(o.matrix_world.translation.z + max(v.co.z for v in o.data.vertices) for o in bpy.data.objects if o.type == "MESH" and o.name.startswith("Wall"))
    # wall points = within 6 cm of any simulated face line (after its shift)
    near_wall = np.zeros(len(xy), bool); tree = cKDTree(xy)
    for f in g["faces"]:
        n = np.array(f["normal"]); p0, p1 = np.array(f["p0"]) + n * fs[f["id"]], np.array(f["p1"]) + n * fs[f["id"]]; d = (p1 - p0) / np.linalg.norm(p1 - p0); L = np.linalg.norm(p1 - p0)
        c = tree.query_ball_point((p0 + p1) / 2, L / 2 + 0.1)
        if not c: continue
        q = xy[c] - p0; t = q @ d; off = q @ n; m = (np.abs(off) < 0.06) & (t > -0.05) & (t < L + 0.05); near_wall[np.array(c)[m]] = True
    # thin to xy_vox so the file stays reviewable
    keep_idx = cKDTree(xy).query(voxel_filter(xy, xy_vox))[1]; keep = np.zeros(len(xy), bool); keep[keep_idx] = True
    w = np.flatnonzero(keep & near_wall); o = np.flatnonzero(keep & ~near_wall)
    levels = np.arange(0.0, ztop + 1e-9, zstep)
    P = [np.c_[np.repeat(xy[w], len(levels), 0), np.tile(levels, len(w))], np.c_[xy[o], np.full(len(o), zs)]]
    measured = [np.where(np.abs(np.tile(levels, len(w)) - zs) < zstep / 2, 1, 0), np.ones(len(o), int)]
    sidv = [np.repeat(sid[w], len(levels)), sid[o]]
    P = np.vstack(P); meas = np.concatenate(measured).astype(np.uint8); sidv = np.concatenate(sidv)
    rng = np.random.default_rng(seed); z0 = P[:, 2].copy(); P[:, 2] = z0 * (1 + zerr * rng.uniform(-1, 1, len(P)))
    path = f"{out}/SIMULATED_3D_point_cloud.ply"
    BM.write_ply(path, P.astype(np.float32), {"is_measured": (meas, "uchar"), "scan_id": (sidv.astype(np.int32), "int")})
    b = open(path, "rb").read().replace(b"comment frame:", b"comment SIMULATED 3D DEMO - extrapolated from 2D at sensor height, NOT MEASURED; z has +-0.8 pct assumed error. frame:", 1); open(path, "wb").write(b)
    print("roof z=%.3f m; %d wall pts x %d levels + %d other = %d points; size %.1f MB" % (ztop, len(w), len(levels), len(o), len(P), os.path.getsize(path) / 1e6))
    import matplotlib; matplotlib.use("Agg"); import matplotlib.pyplot as plt
    sub = P[::max(1, len(P) // 120000)]; fig = plt.figure(figsize=(15, 10)); ax = fig.add_subplot(111, projection="3d")
    ax.scatter(sub[:, 0], sub[:, 1], sub[:, 2], s=0.15, c=sub[:, 2], cmap="viridis", linewidths=0); ax.set_box_aspect((np.ptp(P[:, 0]), np.ptp(P[:, 1]), np.ptp(P[:, 2]) * 2.5)); ax.view_init(35, -60)
    ax.set_title("SIMULATED 3D point cloud (extrapolated from simulated 2D, z error ±0.8 %) – NOT MEASURED DATA", color="#d00"); ax.text2D(0.5, 0.5, "SIMULATED – NOT MEASURED DATA", transform=ax.transAxes, fontsize=34, color="#d00", alpha=0.18, ha="center", rotation=20)
    fig.savefig(f"{out}/SIMULATED_3D_point_cloud_preview.png", dpi=100, bbox_inches="tight")
    return P


if __name__ == "__main__":
    root = os.path.dirname(os.path.abspath(__file__)); run(root, os.path.join(root, "..", "poc_demo_files_output", "simulated_survey_grade"))
