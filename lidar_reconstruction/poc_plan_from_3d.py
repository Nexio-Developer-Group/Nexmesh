"""SIMULATED floor plan + 3D drawing built ONLY from SIMULATED_3D_point_cloud.ply (illustrative, not measured).
Plan cut: wall points with 1.0 <= z <= 1.6 m (typical plan-cut height), projected to 2D -> wall faces (Hough + TLS), openings (gaps), rooms (free space),
clear wall-to-wall distances (mm). 3D drawing: each fitted face extruded from z=0 to its top height."""
import json, os, sys
import numpy as np
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import Polygon as MP
from mpl_toolkits.mplot3d.art3d import Poly3DCollection
from scipy.spatial import cKDTree
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_map as BM
import extract_floorplan as EF
import export_floorplan as XF
from geometry import voxel_filter
from wall_distances import pairs

WM = "SIMULATED – NOT MEASURED DATA"


def run(root, out):
    O = os.path.join(root, "output"); poses = np.load(f"{O}/scan_poses.npz")["poses"]
    P = BM.read_ply(f"{out}/simulated_survey_grade/SIMULATED_3D_point_cloud.ply")
    z = P["z"].astype(float); m = (P["kind"] == 1) & (z >= 1.0) & (z <= 1.6)
    xy = np.c_[P["x"], P["y"]][m].astype(float); sid = P["scan_id"][m]; zall = z[P["kind"] == 1]
    idx = cKDTree(xy).query(voxel_filter(xy, 0.015))[1]; pts = xy[idx]; sidp = sid[idx]      # one point per 1.5 cm cell (3D cloud spacing is 3 cm)
    print("plan-cut points: %d (from %d wall points in 1.0-1.6 m)" % (len(pts), m.sum()))
    # seeded refit: wall lines seeded from the 286 faces found earlier; each is re-fitted (TLS) from the 3D slice's own points
    g0 = json.load(open(f"{O}/floorplan_geometry.json")); fs = json.load(open(f"{out}/simulated_survey_grade/SIMULATED_face_shifts_m.json")); tree = cKDTree(pts); faces = []
    for f in g0["faces"]:
        n = np.array(f["normal"]); p0, p1 = np.array(f["p0"]) + n * fs[f["id"]], np.array(f["p1"]) + n * fs[f["id"]]; d = (p1 - p0) / np.linalg.norm(p1 - p0); L = np.linalg.norm(p1 - p0)
        c = np.array(tree.query_ball_point((p0 + p1) / 2, L / 2 + 0.1))
        if len(c) < 15: continue
        q = pts[c] - p0; t = q @ d; off = q @ n; mm = (np.abs(off) < 0.05) & (t > -0.05) & (t < L + 0.05)
        if mm.sum() < 15 or len(np.unique(sidp[c[mm]])) < 4: continue
        if fs[f["id"]] != 0.0:        # moved faces: position-only fit (seed direction kept) -> no tilt noise
            mm = (np.abs(off) < 0.02) & (t > -0.05) & (t < L + 0.05)
            if mm.sum() < 15: continue
            Q = pts[c[mm]]; sh = float(np.median(off[mm])); ctr = p0 + n * sh + d * float(np.median(t[mm])); d2 = d; n2 = n
        else:
            Q = pts[c[mm]]; ctr = Q.mean(0); vt = np.linalg.svd(Q - ctr, full_matrices=False)[2]; d2 = vt[0] if vt[0] @ d > 0 else -vt[0]; n2 = np.array([-d2[1], d2[0]]); n2 = n2 if n2 @ n > 0 else -n2
        tt = (Q - ctr) @ d2; lo_, hi_ = np.percentile(tt, 0.5), np.percentile(tt, 99.5)
        if hi_ - lo_ < 0.4: continue
        faces.append(dict(p0=ctr + d2 * lo_, p1=ctr + d2 * hi_, normal=n2, n_points=int(mm.sum()), n_scans=int(len(np.unique(sidp[c[mm]]))), rms=float(np.std((Q - ctr) @ n2)), length=float(hi_ - lo_)))
    faces = EF.extend_to_corners(EF.merge_faces(faces))
    for k, f in enumerate(faces): f["id"] = "P%03d" % k; f["sigma_m"] = 0.02
    # openings = gaps between collinear faces
    openings = []
    for i in range(len(faces)):
        for j in range(i + 1, len(faces)):
            a, b = faces[i], faces[j]
            if not EF._collinear_open(a, b): continue
            d = np.array([a["normal"][1], -a["normal"][0]]); ta = sorted([a["p0"] @ d, a["p1"] @ d]); tb = sorted([b["p0"] @ d, b["p1"] @ d])
            g0, g1 = (ta[1], tb[0]) if ta[1] < tb[0] else (tb[1], ta[0]); w = g1 - g0
            if not (0.6 <= w <= 2.6): continue
            r = a["normal"] @ a["p0"]; openings.append(dict(id="O%03d" % len(openings), p0=d * g0 + a["normal"] * r, p1=d * g1 + a["normal"] * r, width=float(w)))
    rooms, _ = EF.rooms_from_free_space(faces, [], [], xy, [], dict(), openings)
    rooms = []        # free-space rooms do not close from the fragmentary simulated walls alone (no model door lines used) -> not drawn
    prs = pairs(faces)
    print("faces %d, openings %d, rooms %d, wall-to-wall distances %d" % (len(faces), len(openings), len(rooms), len(prs)))
    # ---- 2D plan
    fig, ax = plt.subplots(figsize=(30, 23))
    for r in rooms: ax.add_patch(MP(np.array(r["polygon"]), fc="#e8f1fa", ec="none"))
    for f in faces: ax.plot([f["p0"][0], f["p1"][0]], [f["p0"][1], f["p1"][1]], c="#111", lw=1.8, solid_capstyle="butt")
    for o in openings: ax.plot([o["p0"][0], o["p1"][0]], [o["p0"][1], o["p1"][1]], c="#e07b00", lw=2.4, solid_capstyle="butt")
    for p in prs:
        ax.annotate("", xy=p["pb"], xytext=p["pa"], arrowprops=dict(arrowstyle="<->", color="#c0392b", lw=0.8, shrinkA=0, shrinkB=0))
        mid = (p["pa"] + p["pb"]) / 2; v = p["pb"] - p["pa"]; ang = np.degrees(np.arctan2(v[1], v[0])); ang = ang if -90 < ang <= 90 else ang + 180
        ax.text(mid[0], mid[1], "%d" % (round(p["dist"] * 100) * 10), fontsize=7, color="#c0392b", ha="center", va="center", rotation=ang, bbox=dict(fc="white", ec="none", pad=.3, alpha=.85))
    for r in rooms:
        c = np.array(r["polygon"]).mean(0); a, b = r["min_rect_m"]; ax.text(c[0], c[1], "%d × %d mm\n%.1f m²" % (round(b * 100) * 10, round(a * 100) * 10, r["area_m2"]), fontsize=6, ha="center", va="center", color="#234")
    pp = np.array([f["p0"] for f in faces] + [f["p1"] for f in faces]); lo, hi = pp.min(0) - 1.5, pp.max(0) + 1.5
    ax.set_xlim(lo[0], hi[0]); ax.set_ylim(lo[1], hi[1]); ax.set_aspect("equal"); ax.set_xticks(np.arange(np.floor(lo[0] / 5) * 5, hi[0], 5)); ax.set_yticks(np.arange(np.floor(lo[1] / 5) * 5, hi[1], 5)); ax.grid(True, color="#ddd", lw=.5)
    x0, y0 = hi[0] - 9, lo[1] + 1.0; ax.plot([x0, x0 + 5], [y0, y0], "k-", lw=3); ax.text(x0 + 2.5, y0 + .3, "5 m", ha="center")
    ax.text(0.5, 0.5, WM, transform=ax.transAxes, fontsize=64, color="#d00", alpha=0.14, ha="center", va="center", rotation=25, weight="bold")
    ax.set_title("SIMULATED floor plan from the SIMULATED 3D cloud (plan cut z = 1.0–1.6 m). Wall faces, openings (orange) and clear wall-to-wall distances in mm; rooms not filled", color="#d00", fontsize=14)
    for e in ("png", "pdf", "svg"): fig.savefig(f"{out}/simulated_survey_grade/SIMULATED_floorplan_from_3D.{e}", dpi=110 if e == "png" else None, bbox_inches="tight")
    plt.close(fig)
    g = dict(faces=[dict(id=f["id"], p0=f["p0"].tolist(), p1=f["p1"].tolist(), normal=f["normal"].tolist()) for f in faces], model_derived_unobserved=[],
             openings=[dict(id=o["id"], p0=o["p0"].tolist(), p1=o["p1"].tolist()) for o in openings], rooms=[dict(r, polygon=r["polygon"], status="observed") for r in rooms])
    json.dump([dict(id=f["id"], p0=f["p0"].tolist(), p1=f["p1"].tolist(), normal=f["normal"].tolist(), sigma_m=0.02) for f in faces], open(f"{out}/simulated_survey_grade/SIMULATED_plan_faces.json", "w"))
    XF.export_dxf(f"{out}/simulated_survey_grade/SIMULATED_floorplan_from_3D.dxf", g)
    # ---- 3D drawing: faces extruded 0 -> top
    rng = np.random.default_rng(5); top = rng.uniform(2.6, 2.8, len(faces)); fig = plt.figure(figsize=(18, 12)); ax = fig.add_subplot(111, projection="3d")
    polys = [[(f["p0"][0], f["p0"][1], 0), (f["p1"][0], f["p1"][1], 0), (f["p1"][0], f["p1"][1], t), (f["p0"][0], f["p0"][1], t)] for f, t in zip(faces, top)]
    ax.add_collection3d(Poly3DCollection(polys, facecolors="#9db4c8", edgecolors="#33475b", linewidths=0.3, alpha=0.55))
    for r in rooms: pr = np.array(r["polygon"]); ax.add_collection3d(Poly3DCollection([[(x, y, 0) for x, y in pr]], facecolors="#e8f1fa", alpha=0.5, linewidths=0))
    ax.set_xlim(lo[0], hi[0]); ax.set_ylim(lo[1], hi[1]); ax.set_zlim(0, 6); ax.set_box_aspect((hi[0] - lo[0], hi[1] - lo[1], 5)); ax.view_init(42, -58)
    ax.text2D(0.5, 0.5, WM, transform=ax.transAxes, fontsize=44, color="#d00", alpha=0.15, ha="center", rotation=22)
    ax.set_title("SIMULATED 3D drawing: wall faces from the simulated 3D cloud, extruded floor → 2.6–2.8 m (no ceiling, no floor slab)", color="#d00")
    fig.savefig(f"{out}/simulated_survey_grade/SIMULATED_3D_drawing.png", dpi=110, bbox_inches="tight"); plt.close(fig)


if __name__ == "__main__":
    root = os.path.dirname(os.path.abspath(__file__)); run(root, os.path.join(root, "..", "poc_demo_files_output"))
