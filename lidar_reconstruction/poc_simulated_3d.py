"""SIMULATED 3D-LiDAR scan derived from the real/simulated 2D data. Illustrative, NOT measured.
Each wall return is re-observed by a virtual multi-beam 3D LiDAR at the REAL sensor position of that scan (same trajectory, same horizontal range):
  64 beams, elevation -30..+30 deg (+-0.15 deg jitter), z_hit = z_sensor(t) + r * tan(elev), z_sensor(t) = REAL height above floor from the downward LRF log at that scan's time  (vertical planar wall assumed)
- hits below the floor (z<0) or above the per-face wall top U(2.6, 2.8) m are dropped (those beams would hit floor/ceiling; no ground/ceiling points are output)
- range noise 5 mm, 70 % random dropout at 3 cm column spacing (tuned so ~80-85 % of wall cells are covered), then a multiplicative z error of random sign and magnitude U(0.6, 0.8) % on every point
Result: near walls are covered over a shorter height than far walls, as with a real spinning LiDAR. Non-wall returns (furniture/glass) stay at the real LRF capture height.
Flags: kind (1 wall, 2 other), is_measured (1 = beam closest to the real horizontal plane / non-wall returns)."""
import json, os, sys
import numpy as np
from scipy.spatial import cKDTree
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_map as BM
from geometry import voxel_filter


def run(root, out, nbeam=64, fov=30.0, xy_vox=0.03, seed=3):
    O = os.path.join(root, "output"); S = out; rng = np.random.default_rng(seed)
    ply = BM.read_ply(f"{S}/SIMULATED_registered_direct_hits.ply"); xy = np.c_[ply["x"], ply["y"]].astype(float); zs = float(ply["z"][0]); sid = ply["scan_id"]
    g = json.load(open(f"{O}/floorplan_geometry.json")); fs = json.load(open(f"{S}/SIMULATED_face_shifts_m.json")); poses = np.load(f"{O}/scan_poses.npz")["poses"]; hz = np.load(f"{O}/scan_height_lrf.npy")   # real sensor height per scan (downward LRF)
    tree = cKDTree(xy); keep = np.zeros(len(xy), bool); keep[tree.query(voxel_filter(xy, xy_vox))[1]] = True
    wall = np.full(len(xy), -1); best = np.full(len(xy), np.inf)
    for k, f in enumerate(g["faces"]):
        n = np.array(f["normal"]); p0, p1 = np.array(f["p0"]) + n * fs[f["id"]], np.array(f["p1"]) + n * fs[f["id"]]; d = (p1 - p0) / np.linalg.norm(p1 - p0); L = np.linalg.norm(p1 - p0)
        c = np.array(tree.query_ball_point((p0 + p1) / 2, L / 2 + 0.1))
        if len(c) == 0: continue
        q = xy[c] - p0; t = q @ d; off = np.abs(q @ n); m = (off < 0.06) & (t > -0.05) & (t < L + 0.05) & (off < best[c]); wall[c[m]] = k; best[c[m]] = off[m]
    w = np.flatnonzero((wall >= 0) & keep); o = np.flatnonzero(keep & (wall < 0))
    top = rng.uniform(2.6, 2.8, len(g["faces"]))
    el0 = np.deg2rad(np.linspace(-fov, fov, nbeam)); mid = np.argmin(np.abs(el0))
    r = np.hypot(*(xy[w] - poses[sid[w], :2]).T)                                          # real horizontal range of each return
    r = np.maximum(r + rng.normal(0, 0.005, len(w)), 0.2)
    el = el0[None, :] + np.deg2rad(rng.normal(0, 0.15, (len(w), nbeam)))
    z = hz[sid[w]][:, None] + r[:, None] * np.tan(el)             # real LRF capture height + beam elevation
    ok = (z >= 0.0) & (z <= top[wall[w]][:, None] + rng.normal(0, 0.02, (len(w), 1))) & (rng.random((len(w), nbeam)) > 0.70)
    ii, jj = np.nonzero(ok); zz = z[ii, jj] * (1 + rng.choice([-1, 1], len(ii)) * rng.uniform(0.006, 0.008, len(ii)))
    Pw = np.c_[xy[w][ii], zz]; kind_w = np.full(len(ii), 1, np.uint8); meas_w = (jj == mid).astype(np.uint8); sid_w = sid[w][ii]
    Po = np.c_[xy[o], hz[sid[o]]]
    P = np.vstack([Pw, Po]).astype(np.float32); kind = np.r_[kind_w, np.full(len(o), 2, np.uint8)]; meas = np.r_[meas_w, np.ones(len(o), np.uint8)]; sidv = np.r_[sid_w, sid[o]].astype(np.int32)
    path = f"{out}/SIMULATED_3D_point_cloud.ply"
    BM.write_ply(path, P, {"kind": (kind, "uchar"), "is_measured": (meas, "uchar"), "scan_id": (sidv, "int")})
    b = open(path, "rb").read().replace(b"comment frame:", b"comment SIMULATED 3D-LIDAR-LIKE DEMO from real sensor positions/ranges - z NOT MEASURED. no ground/ceiling points. walls 2.6-2.8 m, z error 0.6-0.8 pct. frame:", 1); open(path, "wb").write(b)
    # wall coverage: share of occupied (face position, height 0..top) 10 cm cells hit at least once
    cov = []
    for k in np.unique(wall[w]):
        sel = (wall[w][ii] == k)
        if sel.sum() < 50: continue
        tt = np.floor(np.hypot(*(Pw[sel, :2] - Pw[sel, :2].min(0)).T) / 0.10).astype(int); zc = np.floor(Pw[sel, 2] / 0.10).astype(int)
        nx = np.unique(tt).size; cov.append(len(set(zip(tt, zc))) / max(nx * (top[k] / 0.10), 1))
    print("points %d (wall %d, other %d); z range %.2f..%.2f m; mean wall-cell coverage %.0f %%; size %.1f MB" % (len(P), len(Pw), len(Po), Pw[:, 2].min(), Pw[:, 2].max(), 100 * np.mean(cov), os.path.getsize(path) / 1e6))
    import matplotlib; matplotlib.use("Agg"); import matplotlib.pyplot as plt
    fig = plt.figure(figsize=(18, 11)); ax = fig.add_subplot(121, projection="3d"); sub = P[rng.choice(len(P), min(len(P), 300000), replace=False)]
    ax.scatter(sub[:, 0], sub[:, 1], sub[:, 2], s=0.1, c=sub[:, 2], cmap="turbo", linewidths=0, rasterized=True); ax.set_box_aspect((np.ptp(P[:, 0]), np.ptp(P[:, 1]), 6)); ax.view_init(32, -62); ax.set_title("oblique view (colour = height, z exaggerated)")
    q = Pw[(Pw[:, 0] > -20) & (Pw[:, 0] < -10) & (Pw[:, 1] > -13) & (Pw[:, 1] < -3)]; ax2 = fig.add_subplot(122, projection="3d"); ax2.scatter(q[:, 0], q[:, 1], q[:, 2], s=0.3, c=q[:, 2], cmap="turbo", linewidths=0, rasterized=True)
    ax2.set_box_aspect((np.ptp(q[:, 0]), np.ptp(q[:, 1]), 3)); ax2.view_init(24, -50); ax2.set_title("close-up: rooms in the left wing")
    fig.suptitle("SIMULATED 3D-LiDAR-like cloud (from real sensor positions, no ground; z error 0.6–0.8 %) – NOT MEASURED DATA", color="#d00", fontsize=14)
    fig.text(0.5, 0.5, "SIMULATED – NOT MEASURED DATA", fontsize=40, color="#d00", alpha=0.15, ha="center", va="center", rotation=20)
    fig.savefig(f"{out}/SIMULATED_3D_point_cloud_preview.png", dpi=100, bbox_inches="tight")


if __name__ == "__main__":
    root = os.path.dirname(os.path.abspath(__file__)); run(root, os.path.join(root, "..", "poc_demo_files_output", "simulated_survey_grade"))
