"""Clear distances (mm) between facing, parallel, LiDAR-observed wall faces; drawn on the real floor plan. Real data only."""
import csv, json, os, sys
import numpy as np
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import Polygon as MP


def seg_cross(p, q, a, b):
    d1, d2 = q - p, b - a; den = d1[0] * d2[1] - d1[1] * d2[0]
    if abs(den) < 1e-12: return False
    t = ((a - p)[0] * d2[1] - (a - p)[1] * d2[0]) / den; u = ((a - p)[0] * d1[1] - (a - p)[1] * d1[0]) / den
    return 0.02 < t < 0.98 and 0 <= u <= 1


def pairs(faces, min_overlap=0.6, dmin=0.5, dmax=8.0):
    F = [dict(p0=np.array(f["p0"]), p1=np.array(f["p1"]), n=np.array(f["normal"]), id=f["id"], sig=f["sigma_m"]) for f in faces]
    out = []
    for i, a in enumerate(F):
        d = np.array([a["n"][1], -a["n"][0]]); ta = sorted([a["p0"] @ d, a["p1"] @ d])
        for j, b in enumerate(F):
            if i == j or a["n"] @ b["n"] > -np.cos(np.deg2rad(3)): continue          # must face each other (opposite normals)
            off = a["n"] @ (b["p0"] / 2 + b["p1"] / 2 - a["p0"])
            if not (dmin <= off <= dmax) or i > j: continue                           # b in front of a; each pair once
            tb = sorted([b["p0"] @ d, b["p1"] @ d]); lo, hi = max(ta[0], tb[0]), min(ta[1], tb[1])
            if hi - lo < min_overlap: continue
            tm = (lo + hi) / 2; pa = a["p0"] + d * (tm - a["p0"] @ d); pb = pa + a["n"] * off
            if any(k not in (i, j) and seg_cross(pa, pb, c["p0"], c["p1"]) for k, c in enumerate(F)): continue   # another wall in between
            out.append(dict(a=a["id"], b=b["id"], pa=pa, pb=pb, dist=off, overlap=hi - lo, sigma=float(np.hypot(a["sig"], b["sig"]))))
    return out


def run(root):
    O = os.path.join(root, "output"); g = json.load(open(f"{O}/floorplan_geometry.json"))
    prs = pairs(g["faces"]); print(len(prs), "wall-to-wall distances")
    with open(f"{O}/wall_distances_mm.csv", "w", newline="") as f:
        w = csv.writer(f); w.writerow(["face_a", "face_b", "distance_mm", "sigma_mm", "overlap_m", "x_a", "y_a", "x_b", "y_b"])
        for p in prs: w.writerow([p["a"], p["b"], round(p["dist"] * 1000), round(p["sigma"] * 1000), "%.2f" % p["overlap"], *("%.3f" % v for v in (*p["pa"], *p["pb"]))])
    fig, ax = plt.subplots(figsize=(34, 26))
    for r in g["rooms"]: ax.add_patch(MP(np.array(r["polygon"]), fc="#eef3f8", ec="none"))
    for f in g["faces"]: ax.plot([f["p0"][0], f["p1"][0]], [f["p0"][1], f["p1"][1]], c="#111", lw=1.6, solid_capstyle="butt")
    for p in prs:
        ax.annotate("", xy=p["pb"], xytext=p["pa"], arrowprops=dict(arrowstyle="<->", color="#c0392b", lw=0.9, shrinkA=0, shrinkB=0))
        m = (p["pa"] + p["pb"]) / 2; v = p["pb"] - p["pa"]; ang = np.degrees(np.arctan2(v[1], v[0])); ang = ang if -90 < ang <= 90 else ang + 180
        ax.text(m[0], m[1], "%d" % (round(p["dist"] * 100) * 10), fontsize=7.5, color="#c0392b", ha="center", va="center", rotation=ang, bbox=dict(fc="white", ec="none", pad=0.4, alpha=0.85))
    pts = np.array([f["p0"] for f in g["faces"]] + [f["p1"] for f in g["faces"]]); lo, hi = pts.min(0) - 1.5, pts.max(0) + 1.5
    ax.set_xlim(lo[0], hi[0]); ax.set_ylim(lo[1], hi[1]); ax.set_aspect("equal"); ax.set_xticks(np.arange(np.floor(lo[0] / 5) * 5, hi[0], 5)); ax.set_yticks(np.arange(np.floor(lo[1] / 5) * 5, hi[1], 5)); ax.grid(True, color="#ddd", lw=0.5)
    ax.set_xlabel("X (m) — Blender world frame"); ax.set_ylabel("Y (m)")
    ax.set_title("REAL data: clear distance between facing LiDAR-observed walls (mm, rounded to 10 mm; uncertainty ≈ ±20–40 mm). %d dimensions." % len(prs), fontsize=14)
    fig.savefig(f"{O}/floorplan_wall_distances_mm.png", dpi=110, bbox_inches="tight"); plt.close(fig)
    return len(prs)


if __name__ == "__main__":
    run(os.path.dirname(os.path.abspath(__file__)))
