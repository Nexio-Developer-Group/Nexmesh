"""Repeat the wall-distance extraction and the model comparison on the SIMULATED survey-grade point cloud (illustrative, not measured)."""
import csv, json, os
import numpy as np
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import Polygon as MP
from matplotlib.lines import Line2D
import build_map as BM
import extract_floorplan as EF
from geometry import voxel_filter
from scipy.spatial import cKDTree
from wall_distances import pairs
from compare_distances_to_model import ray_hit


def refit_faces(pts, fs, g):
    """Unchanged faces (shift 0) are used exactly as measured (real data untouched). Moved faces: position-only fit (median offset
    of the nearby simulated points along the original normal), original direction kept, so tilt noise does not leak into distances."""
    tree = cKDTree(pts); faces = []
    for f in g["faces"]:
        n = np.array(f["normal"]); p0, p1 = np.array(f["p0"]), np.array(f["p1"])
        if fs[f["id"]] == 0.0:
            faces.append(dict(id=f["id"], p0=p0, p1=p1, normal=n, sigma_m=0.01)); continue
        q0, q1 = p0 + n * fs[f["id"]], p1 + n * fs[f["id"]]; d = (q1 - q0) / np.linalg.norm(q1 - q0); L = np.linalg.norm(q1 - q0)
        cand = tree.query_ball_point((q0 + q1) / 2, L / 2 + 0.1)
        q = pts[cand] - q0; t = q @ d; off = q @ n; m = (np.abs(off) < 0.02) & (t > -0.05) & (t < L + 0.05)
        if m.sum() < 15: continue
        shift = float(np.median(off[m]))
        faces.append(dict(id=f["id"], p0=q0 + n * shift, p1=q1 + n * shift, normal=n, sigma_m=0.01))
    return faces


def measure(faces, msegs):
    rows = []
    for p in pairs(faces):
        n = (p["pb"] - p["pa"]) / np.linalg.norm(p["pb"] - p["pa"]); mid = (p["pa"] + p["pb"]) / 2; h = p["dist"] / 2
        t1 = ray_hit(mid, n, msegs, h + 0.15); t2 = ray_hit(mid, -n, msegs, h + 0.15)
        ok = t1 is not None and t2 is not None and abs(t1 - h) < 0.15 and abs(t2 - h) < 0.15
        rows.append(dict(a=p["a"], b=p["b"], pa=p["pa"], pb=p["pb"], lidar=p["dist"] * 1000, model=(t1 + t2) * 1000 if ok else None))
    for r in rows: r["delta"] = None if r["model"] is None else r["lidar"] - r["model"]
    return rows


def run(root, out):
    O = os.path.join(root, "output"); S = os.path.join(out, "simulated_survey_grade")
    ply = BM.read_ply(f"{S}/SIMULATED_registered_direct_hits.ply"); pts = np.c_[ply["x"], ply["y"]].astype(float)
    g = json.load(open(f"{O}/floorplan_geometry.json")); seg = json.load(open(f"{O}/model_slice.json"))
    fs = json.load(open(f"{S}/SIMULATED_face_shifts_m.json")); faces = refit_faces(pts, fs, g)
    print(len(faces), "faces refitted (real: %d)" % len(g["faces"]))
    msegs = [(np.array(s["p"][:2]), np.array(s["p"][2:])) for s in seg if s["kind"] in ("Wall", "Column", "Window")]
    rows = measure(faces, msegs)
    for r in rows: r["delta"] = None if r["model"] is None else r["lidar"] - r["model"]
    with open(f"{S}/SIMULATED_wall_distances_vs_model_mm.csv", "w", newline="") as f:
        w = csv.writer(f); w.writerow(["face_a", "face_b", "SIMULATED_mm", "model_mm", "delta_mm"])
        for r in rows: w.writerow([r["a"], r["b"], round(r["lidar"]), "" if r["model"] is None else round(r["model"]), "" if r["delta"] is None else round(r["delta"])])
    d = np.array([r["delta"] for r in rows if r["delta"] is not None]); ad = np.abs(d)
    st = dict(distances=len(rows), compared=len(d), mean_signed_mm=float(d.mean()), median_abs_mm=float(np.median(ad)), p90_abs_mm=float(np.percentile(ad, 90)), max_abs_mm=float(ad.max()),
              within_30=float((ad <= 30).mean()), within_60=float((ad <= 60).mean()), note="SIMULATED cloud: not measured data")
    json.dump(st, open(f"{S}/SIMULATED_wall_distances_vs_model_summary.json", "w"), indent=1); print(st)
    fig, ax = plt.subplots(figsize=(34, 26))
    for r in g["rooms"]: ax.add_patch(MP(np.array(r["polygon"]), fc="#f1f4f7", ec="none"))
    for f in faces: ax.plot([f["p0"][0], f["p1"][0]], [f["p0"][1], f["p1"][1]], c="#111", lw=1.3, solid_capstyle="butt")
    for s_ in msegs: ax.plot([s_[0][0], s_[1][0]], [s_[0][1], s_[1][1]], c="#999", lw=0.5, zorder=0)
    col = lambda x: "#1a9a1a" if abs(x) <= 30 else ("#1f5fe0" if abs(x) <= 60 else "#e02020")
    for r in rows:
        v = r["pb"] - r["pa"]; ang = np.degrees(np.arctan2(v[1], v[0])); ang = ang if -90 < ang <= 90 else ang + 180; m = (r["pa"] + r["pb"]) / 2
        if r["delta"] is None:
            ax.annotate("", xy=r["pb"], xytext=r["pa"], arrowprops=dict(arrowstyle="<->", color="#888", lw=0.8, ls="--")); ax.text(m[0], m[1], "%d\nno model match" % (round(r["lidar"] / 10) * 10), fontsize=6.5, color="#666", ha="center", va="center", rotation=ang, bbox=dict(fc="white", ec="none", pad=.3, alpha=.85))
        else:
            c = col(r["delta"]); ax.annotate("", xy=r["pb"], xytext=r["pa"], arrowprops=dict(arrowstyle="<->", color=c, lw=1.0))
            ax.text(m[0], m[1], "S %d\nM %d\nΔ %+d" % (round(r["lidar"]), round(r["model"]), round(r["delta"])), fontsize=6.5, color=c, ha="center", va="center", rotation=ang, bbox=dict(fc="white", ec="none", pad=.3, alpha=.9))
    ax.legend(handles=[Line2D([], [], c="#1a9a1a", lw=3, label="|Δ| ≤ 30 mm: %.0f%%" % (100 * st["within_30"])), Line2D([], [], c="#1f5fe0", lw=3, label="30–60 mm: %.0f%%" % (100 * (st["within_60"] - st["within_30"]))),
                       Line2D([], [], c="#e02020", lw=3, label=">60 mm: %.0f%%" % (100 * (1 - st["within_60"]))), Line2D([], [], c="#888", lw=2, ls="--", label="no matching model wall pair (%d)" % (len(rows) - len(d)))], loc="lower left", fontsize=12)
    pp = np.array([f["p0"] for f in faces] + [f["p1"] for f in faces]); lo, hi = pp.min(0) - 1.5, pp.max(0) + 1.5
    ax.set_xlim(lo[0], hi[0]); ax.set_ylim(lo[1], hi[1]); ax.set_aspect("equal"); ax.grid(True, color="#e3e3e3", lw=.5)
    ax.text(0.5, 0.5, "SIMULATED – NOT MEASURED DATA", transform=ax.transAxes, fontsize=70, color="#d00", alpha=0.15, ha="center", va="center", rotation=25, weight="bold")
    ax.set_title("SIMULATED survey-grade cloud (assumed spec, by construction): wall-to-wall distance S vs Blender model M, mm.  median |Δ| %.0f mm, mean %+.0f mm (n=%d)" % (st["median_abs_mm"], st["mean_signed_mm"], st["compared"]), fontsize=14, color="#d00")
    fig.savefig(f"{S}/SIMULATED_floorplan_wall_distances_vs_model_mm.png", dpi=110, bbox_inches="tight"); plt.close(fig)


if __name__ == "__main__":
    root = os.path.dirname(os.path.abspath(__file__)); run(root, os.path.join(root, "..", "poc_demo_files_output"))
