"""Compare every LiDAR wall-to-wall distance with the same distance read from the Blender model section (z = sensor height)."""
import csv, json, os
import numpy as np
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import Polygon as MP
from matplotlib.lines import Line2D
from wall_distances import pairs


def ray_hit(o, d, segs, maxd=1.0):
    """Nearest hit of ray o + t d (t>0) with segments roughly perpendicular to d."""
    best = None
    for a, b in segs:
        e = b - a; L = np.linalg.norm(e)
        if L < 1e-6 or abs((e / L) @ d) > 0.2: continue          # must be ~perpendicular to the ray
        den = d[0] * e[1] - d[1] * e[0]
        if abs(den) < 1e-9: continue
        t = ((a - o)[0] * e[1] - (a - o)[1] * e[0]) / den; u = ((a - o)[0] * d[1] - (a - o)[1] * d[0]) / den
        if 0 < t < maxd and 0 <= u <= 1 and (best is None or t < best): best = t
    return best


def run(root):
    O = os.path.join(root, "output"); g = json.load(open(f"{O}/floorplan_geometry.json")); seg = json.load(open(f"{O}/model_slice.json"))
    msegs = [(np.array(s["p"][:2]), np.array(s["p"][2:])) for s in seg if s["kind"] in ("Wall", "Column", "Window")]
    rows = []
    for p in pairs(g["faces"]):
        n = (p["pb"] - p["pa"]) / np.linalg.norm(p["pb"] - p["pa"]); mid = (p["pa"] + p["pb"]) / 2; h = p["dist"] / 2
        t1 = ray_hit(mid, n, msegs, h + 0.15); t2 = ray_hit(mid, -n, msegs, h + 0.15)
        # accept only if both model hits lie within 15 cm of the LiDAR faces (else a different wall)
        ok = t1 is not None and t2 is not None and abs(t1 - h) < 0.15 and abs(t2 - h) < 0.15
        rows.append(dict(a=p["a"], b=p["b"], pa=p["pa"], pb=p["pb"], lidar=p["dist"] * 1000, model=(t1 + t2) * 1000 if ok else None, sigma=p["sigma"] * 1000))
    for r in rows: r["delta"] = None if r["model"] is None else r["lidar"] - r["model"]
    with open(f"{O}/wall_distances_lidar_vs_model_mm.csv", "w", newline="") as f:
        w = csv.writer(f); w.writerow(["face_a", "face_b", "lidar_mm", "model_mm", "delta_mm(lidar-model)", "lidar_sigma_mm"])
        for r in rows: w.writerow([r["a"], r["b"], round(r["lidar"]), "" if r["model"] is None else round(r["model"]), "" if r["delta"] is None else round(r["delta"]), round(r["sigma"])])
    d = np.array([r["delta"] for r in rows if r["delta"] is not None]); ad = np.abs(d)
    stats = dict(total=len(rows), compared=len(d), no_model_match=len(rows) - len(d), mean_signed_mm=float(d.mean()), median_abs_mm=float(np.median(ad)), p90_abs_mm=float(np.percentile(ad, 90)), max_abs_mm=float(ad.max()),
                 within_20=float((ad <= 20).mean()), within_30=float((ad <= 30).mean()), within_60=float((ad <= 60).mean()), rms_mm=float(np.sqrt((d ** 2).mean())))
    json.dump(stats, open(f"{O}/wall_distances_lidar_vs_model_summary.json", "w"), indent=1); print(stats)
    fig, ax = plt.subplots(figsize=(34, 26))
    for r in g["rooms"]: ax.add_patch(MP(np.array(r["polygon"]), fc="#f1f4f7", ec="none"))
    for f in g["faces"]: ax.plot([f["p0"][0], f["p1"][0]], [f["p0"][1], f["p1"][1]], c="#111", lw=1.3, solid_capstyle="butt")
    for s_ in msegs: ax.plot([s_[0][0], s_[1][0]], [s_[0][1], s_[1][1]], c="#999", lw=0.5, zorder=0)
    col = lambda x: "#1a9a1a" if abs(x) <= 30 else ("#1f5fe0" if abs(x) <= 60 else "#e02020")
    for r in rows:
        v = r["pb"] - r["pa"]; ang = np.degrees(np.arctan2(v[1], v[0])); ang = ang if -90 < ang <= 90 else ang + 180; m = (r["pa"] + r["pb"]) / 2
        if r["delta"] is None:
            ax.annotate("", xy=r["pb"], xytext=r["pa"], arrowprops=dict(arrowstyle="<->", color="#888", lw=0.8, ls="--")); ax.text(m[0], m[1], "%d\nno model match" % (round(r["lidar"] / 10) * 10), fontsize=6.5, color="#666", ha="center", va="center", rotation=ang, bbox=dict(fc="white", ec="none", pad=.3, alpha=.85))
        else:
            c = col(r["delta"]); ax.annotate("", xy=r["pb"], xytext=r["pa"], arrowprops=dict(arrowstyle="<->", color=c, lw=1.0))
            ax.text(m[0], m[1], "L %d\nM %d\nΔ %+d" % (round(r["lidar"]), round(r["model"]), round(r["delta"])), fontsize=6.5, color=c, ha="center", va="center", rotation=ang, bbox=dict(fc="white", ec="none", pad=.3, alpha=.9))
    ax.legend(handles=[Line2D([], [], c="#1a9a1a", lw=3, label="|Δ| ≤ 30 mm: %.0f%% of compared" % (100 * stats["within_30"])), Line2D([], [], c="#1f5fe0", lw=3, label="30–60 mm: %.0f%%" % (100 * (stats["within_60"] - stats["within_30"]))),
                       Line2D([], [], c="#e02020", lw=3, label=">60 mm: %.0f%%" % (100 * (1 - stats["within_60"]))), Line2D([], [], c="#888", lw=2, ls="--", label="no matching model wall pair (%d)" % stats["no_model_match"])], loc="lower left", fontsize=12)
    pts = np.array([f["p0"] for f in g["faces"]] + [f["p1"] for f in g["faces"]]); lo, hi = pts.min(0) - 1.5, pts.max(0) + 1.5
    ax.set_xlim(lo[0], hi[0]); ax.set_ylim(lo[1], hi[1]); ax.set_aspect("equal"); ax.grid(True, color="#e3e3e3", lw=.5)
    ax.set_title("REAL data: LiDAR (L) vs Blender model (M) wall-to-wall distance, mm. Δ = L − M.  median |Δ| %.0f mm, p90 %.0f mm, mean %+.0f mm (n=%d)" % (stats["median_abs_mm"], stats["p90_abs_mm"], stats["mean_signed_mm"], stats["compared"]), fontsize=14)
    fig.savefig(f"{O}/floorplan_wall_distances_lidar_vs_model_mm.png", dpi=110, bbox_inches="tight"); plt.close(fig)


if __name__ == "__main__":
    run(os.path.dirname(os.path.abspath(__file__)))
