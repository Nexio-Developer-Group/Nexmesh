"""SIMULATED 3D cloud (looks like a 3D-LiDAR scan) built ONLY from the simulated 2D cloud. Illustrative, NOT measured.
- ground: floor points at z = 0 over rooms and a 0.8 m band around the walked path (additive noise 3 mm)
- walls: every simulated wall point is extended from z = 0 up to a per-face top height drawn from U(2.6, 2.8) m (+-2 cm roughness), 5 cm vertical spacing
- coverage: 15-20 % of each wall face's length is left as sensor-height-only gaps (occlusions / openings) -> 80-85 % of the wall length is full height
- z error: every wall point gets a multiplicative z error of random sign and magnitude U(0.6, 0.8) %
Flags: kind (0 floor, 1 wall, 2 other), is_measured (1 only for points at the real sensor height 1.49 m)."""
import json, os, sys
import numpy as np
from scipy.spatial import cKDTree
from shapely.geometry import Polygon, Point, LineString
from shapely.ops import unary_union
from shapely import contains_xy
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_map as BM
from geometry import voxel_filter


def run(root, out, zstep=0.05, xy_vox=0.05, seed=3):
    O = os.path.join(root, "output"); S = out; rng = np.random.default_rng(seed)
    ply = BM.read_ply(f"{S}/SIMULATED_registered_direct_hits.ply"); xy = np.c_[ply["x"], ply["y"]].astype(float); zs = float(ply["z"][0]); sid = ply["scan_id"]
    g = json.load(open(f"{O}/floorplan_geometry.json")); fs = json.load(open(f"{S}/SIMULATED_face_shifts_m.json")); poses = np.load(f"{O}/scan_poses.npz")["poses"]
    tree = cKDTree(xy); keep = np.zeros(len(xy), bool); keep[tree.query(voxel_filter(xy, xy_vox))[1]] = True
    wall = np.full(len(xy), -1); wt = np.zeros(len(xy)); best = np.full(len(xy), np.inf)
    for k, f in enumerate(g["faces"]):
        n = np.array(f["normal"]); p0, p1 = np.array(f["p0"]) + n * fs[f["id"]], np.array(f["p1"]) + n * fs[f["id"]]; d = (p1 - p0) / np.linalg.norm(p1 - p0); L = np.linalg.norm(p1 - p0)
        c = np.array(tree.query_ball_point((p0 + p1) / 2, L / 2 + 0.1))
        if len(c) == 0: continue
        q = xy[c] - p0; t = q @ d; off = np.abs(q @ n); m = (off < 0.06) & (t > -0.05) & (t < L + 0.05) & (off < best[c])
        wall[c[m]] = k; wt[c[m]] = t[m]; best[c[m]] = off[m]
    is_wall = (wall >= 0) & keep; w = np.flatnonzero(is_wall); o = np.flatnonzero(keep & (wall < 0))
    # per-face top height and gaps (15-20 % of the length)
    top = rng.uniform(2.6, 2.8, len(g["faces"])); gaps = {}
    for k, f in enumerate(g["faces"]):
        L = np.linalg.norm(np.array(f["p1"]) - np.array(f["p0"])); frac = rng.uniform(0.15, 0.20); iv = []; tot = 0.0
        while tot < frac * L and L > 1.0:
            gl = min(rng.uniform(0.4, 1.2), frac * L - tot + 0.05); s0 = rng.uniform(0, max(L - gl, 0.01)); iv.append((s0, s0 + gl)); tot += gl
        gaps[k] = iv
    Z, X, K, M, SID = [], [], [], [], []
    for i in w:
        k = wall[i]; in_gap = any(a <= wt[i] <= b for a, b in gaps[k])
        if in_gap: zs_ = np.array([zs]); 
        else: zs_ = np.arange(0.0, top[k] + rng.normal(0, 0.02), zstep); zs_ = np.unique(np.r_[zs_, zs])
        err = rng.choice([-1, 1], len(zs_)) * rng.uniform(0.006, 0.008, len(zs_))
        z = zs_ * (1 + err); z[np.isclose(zs_, zs)] = zs * (1 + err[np.isclose(zs_, zs)])
        Z.append(z); X.append(np.repeat(xy[i][None], len(z), 0)); K.append(np.full(len(z), 1)); M.append(np.isclose(zs_, zs).astype(np.uint8)); SID.append(np.full(len(z), sid[i]))
    # other returns (furniture/clutter/glass) stay at the real sensor height
    X.append(xy[o]); Z.append(np.full(len(o), zs)); K.append(np.full(len(o), 2)); M.append(np.ones(len(o), np.uint8)); SID.append(sid[o])
    # ground: rooms + band around the walked path
    area = unary_union([Polygon(r["polygon"]) for r in g["rooms"]] + [LineString(poses[::20, :2]).buffer(0.8)])
    lo, hi = np.array(area.bounds[:2]), np.array(area.bounds[2:]); gx, gy = np.meshgrid(np.arange(lo[0], hi[0], 0.10), np.arange(lo[1], hi[1], 0.10)); G = np.c_[gx.ravel(), gy.ravel()]
    G = G[contains_xy(area, G[:, 0], G[:, 1])]
    X.append(G); Z.append(rng.normal(0, 0.003, len(G))); K.append(np.zeros(len(G), int)); M.append(np.zeros(len(G), np.uint8)); SID.append(np.full(len(G), -1))
    P = np.c_[np.vstack(X), np.concatenate(Z)].astype(np.float32); kind = np.concatenate(K).astype(np.uint8); meas = np.concatenate(M).astype(np.uint8); sidv = np.concatenate(SID).astype(np.int32)
    path = f"{out}/SIMULATED_3D_point_cloud.ply"
    BM.write_ply(path, P, {"kind": (kind, "uchar"), "is_measured": (meas, "uchar"), "scan_id": (sidv, "int")})
    b = open(path, "rb").read().replace(b"comment frame:", b"comment SIMULATED 3D-LIDAR-LIKE DEMO - z extrapolated from 2D, NOT MEASURED. ground z=0, walls 2.6-2.8 m, 80-85 pct wall coverage, z error 0.6-0.8 pct. frame:", 1); open(path, "wb").write(b)
    wz = P[kind == 1, 2]
    print("points %d (floor %d, wall %d, other %d); wall z max %.2f m, size %.1f MB" % (len(P), (kind == 0).sum(), (kind == 1).sum(), (kind == 2).sum(), wz.max(), os.path.getsize(path) / 1e6))
    import matplotlib; matplotlib.use("Agg"); import matplotlib.pyplot as plt
    fig = plt.figure(figsize=(18, 11)); ax = fig.add_subplot(121, projection="3d"); sub = P[rng.choice(len(P), min(len(P), 250000), replace=False)]
    ax.scatter(sub[:, 0], sub[:, 1], sub[:, 2], s=0.12, c=sub[:, 2], cmap="turbo", linewidths=0, rasterized=True); ax.set_box_aspect((np.ptp(P[:, 0]), np.ptp(P[:, 1]), 6)); ax.view_init(38, -62); ax.set_title("oblique view (colour = height, z exaggerated)")
    ax2 = fig.add_subplot(122); ax2.scatter(sub[:, 0], sub[:, 1], s=0.12, c=sub[:, 2], cmap="turbo", linewidths=0, rasterized=True); ax2.set_aspect("equal"); ax2.set_title("top view (colour = height 0–2.8 m)")
    fig.suptitle("SIMULATED 3D-LiDAR-like point cloud (extrapolated from simulated 2D; z error 0.6–0.8 %) – NOT MEASURED DATA", color="#d00", fontsize=14)
    fig.text(0.5, 0.5, "SIMULATED – NOT MEASURED DATA", fontsize=40, color="#d00", alpha=0.15, ha="center", va="center", rotation=20)
    fig.savefig(f"{out}/SIMULATED_3D_point_cloud_preview.png", dpi=100, bbox_inches="tight")


if __name__ == "__main__":
    root = os.path.dirname(os.path.abspath(__file__)); run(root, os.path.join(root, "..", "poc_demo_files_output", "simulated_survey_grade"))
