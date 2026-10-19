"""Outputs for the CAD-referenced SIMULATED readings: DXF (plan from the simulated 3D cloud + only the target readings), CSV, comparison map, summary. SIMULATED - NOT MEASURED."""
import csv, json, os, sys
import numpy as np
import ezdxf
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.lines import Line2D
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import cad_extract

BIN = lambda d: "green" if abs(d) <= 7 else ("blue" if abs(d) <= 15 else "red")
COL = {"green": "#1a9a1a", "blue": "#1f5fe0", "red": "#e02020"}


def run(root, out):
    O = os.path.join(root, "output"); S = os.path.join(out, "simulated_survey_grade"); g = json.load(open(f"{O}/floorplan_geometry.json")); F = {f["id"]: f for f in g["faces"]}
    T = json.load(open(f"{S}/SIMULATED_target_readings.json")); fs = json.load(open(f"{S}/SIMULATED_face_shifts_override.json")); faces = json.load(open(f"{S}/SIMULATED_plan_faces.json"))
    T = [t for t in T if t.get("sim_measured") is not None]
    for t in T:
        t["delta_meas"] = t["sim_measured"] - t["cad"]; t["bin"] = BIN(t["delta_meas"]); na = np.array(F[t["a"]]["normal"])
        t["pa2"] = (np.array(t["pa"]) + na * fs[t["a"]]).tolist(); t["pb2"] = (np.array(t["pb"]) - na * fs[t["b"]]).tolist()
    cnt = {k: sum(t["bin"] == k for t in T) for k in ("green", "blue", "red")}; print("readings:", len(T), cnt)
    # ---- DXF
    doc = ezdxf.new("R2018", setup=True); doc.units = ezdxf.units.M; msp = doc.modelspace()
    for name, col in (("SIM_WALL_FACES", 8), ("SIM_DIM_GREEN_LE7MM", 3), ("SIM_DIM_BLUE_7_15MM", 5), ("SIM_DIM_RED_GT15MM", 1), ("SIM_NOTE", 1)): doc.layers.add(name, color=col)
    for f in faces: msp.add_line(f["p0"], f["p1"], dxfattribs={"layer": "SIM_WALL_FACES"})
    for t in T:
        lay = {"green": "SIM_DIM_GREEN_LE7MM", "blue": "SIM_DIM_BLUE_7_15MM", "red": "SIM_DIM_RED_GT15MM"}[t["bin"]]; pa, pb = np.array(t["pa2"]), np.array(t["pb2"]); v = (pb - pa) / np.linalg.norm(pb - pa); nn = np.array([-v[1], v[0]])
        msp.add_line(tuple(pa), tuple(pb), dxfattribs={"layer": lay})
        for pt in (pa, pb): msp.add_line(tuple(pt - nn * 0.08), tuple(pt + nn * 0.08), dxfattribs={"layer": lay})
        ang = np.degrees(np.arctan2(v[1], v[0])); ang = ang if -90 < ang <= 90 else ang + 180
        msp.add_text("%d  (CAD %d, d%+d)" % (round(t["sim_measured"]), round(t["cad"]), round(t["delta_meas"])), height=0.10, rotation=ang, dxfattribs={"layer": lay}).set_placement(tuple((pa + pb) / 2 + nn * 0.12), align=ezdxf.enums.TextEntityAlignment.MIDDLE_CENTER)
    allp = np.array([f["p0"] for f in faces] + [f["p1"] for f in faces]); x0, y0 = allp.min(0)
    msp.add_text("SIMULATED - NOT MEASURED DATA: in-room wall-to-wall distances (mm) from the simulated 3D LiDAR cloud vs real CAD. green <=7 mm, blue 7-15 mm, red >15 mm", height=0.35, dxfattribs={"layer": "SIM_NOTE"}).set_placement((x0, y0 - 1.5))
    for d in (S, out): doc.saveas(os.path.join(d, "SIMULATED_3d_lidar_distance_btw_walls.dxf"))
    # ---- CSV + summary
    with open(f"{S}/SIMULATED_3d_lidar_distance_btw_walls.csv", "w", newline="") as f:
        w = csv.writer(f); w.writerow(["face_a", "face_b", "room", "cad_mm", "real_lidar_mm", "real_lidar_minus_cad", "simulated_mm", "simulated_minus_cad", "bin", "reading_type"])
        for t in T: w.writerow([t["a"], t["b"], t["room"] or "hall", round(t["cad"]), round(t["lidar"]), round(t["lidar"] - t["cad"]), round(t["sim_measured"]), round(t["delta_meas"]), t["bin"], t["kind"]])
    d = np.array([t["delta_meas"] for t in T]); a = np.abs(d); dr = np.abs([t["lidar"] - t["cad"] for t in T])
    summ = dict(readings=len(T), bins=cnt, median_abs_mm=float(np.median(a)), p90_abs_mm=float(np.percentile(a, 90)), max_abs_mm=float(a.max()), real_lidar_median_abs_mm=float(np.median(dr)),
                red_readings=[dict(pair=t["a"] + "-" + t["b"], cad=t["cad"], simulated=round(t["sim_measured"]), delta=round(t["delta_meas"]), room=t["room"] or "hall") for t in T if t["bin"] == "red"],
                note="SIMULATED: green/blue values are set by an assumed scanner spec; red readings are the kept/same-type deviations. Hallway readings removed.")
    json.dump(summ, open(f"{out}/COMPARISON_cad_vs_simulated_summary.json", "w"), indent=1)
    # ---- map
    r = json.load(open(f"{O}/cad_registration.json")); R = np.array(r["R"]); tt = np.array(r["t"]); sc = r["scale_m_per_pt"]; Sp, _, _ = cad_extract.extract(os.path.join(root, "data", "cad_plan.pdf")); A = sc * Sp[:, :2] @ R.T + tt; B = sc * Sp[:, 2:] @ R.T + tt
    fig, ax = plt.subplots(figsize=(34, 26))
    for a_, b_ in zip(A, B):
        if np.hypot(*(b_ - a_)) < 40: ax.plot([a_[0], b_[0]], [a_[1], b_[1]], c="#c9c9c9", lw=0.3, zorder=0)
    for f in faces: ax.plot([f["p0"][0], f["p1"][0]], [f["p0"][1], f["p1"][1]], c="#222", lw=1.3, solid_capstyle="butt", zorder=1)
    for t in T:
        pa, pb = np.array(t["pa2"]), np.array(t["pb2"]); v = pb - pa; ang = np.degrees(np.arctan2(v[1], v[0])); ang = ang if -90 < ang <= 90 else ang + 180; m = (pa + pb) / 2; c = COL[t["bin"]]
        ax.annotate("", xy=pb, xytext=pa, arrowprops=dict(arrowstyle="<->", color=c, lw=1.4, shrinkA=0, shrinkB=0), zorder=3)
        ax.text(m[0], m[1], "CAD %d\nSIM %d\nΔ %+d" % (round(t["cad"]), round(t["sim_measured"]), round(t["delta_meas"])), fontsize=8, color=c, ha="center", va="center", rotation=ang, bbox=dict(fc="white", ec=c, lw=.4, pad=.3, alpha=.93), zorder=4)
    lo, hi = allp.min(0) - 2, allp.max(0) + 2; ax.set_xlim(lo[0], hi[0]); ax.set_ylim(lo[1], hi[1]); ax.set_aspect("equal"); ax.grid(True, color="#e6e6e6", lw=.5)
    ax.legend(handles=[Line2D([], [], c=COL["green"], lw=3, label="≤ 7 mm: %d" % cnt["green"]), Line2D([], [], c=COL["blue"], lw=3, label="7–15 mm: %d" % cnt["blue"]), Line2D([], [], c=COL["red"], lw=3, label="> 15 mm: %d" % cnt["red"])], loc="lower left", fontsize=14)
    ax.text(0.5, 0.5, "SIMULATED – NOT MEASURED DATA", transform=ax.transAxes, fontsize=70, color="#d00", alpha=0.10, ha="center", va="center", rotation=25, weight="bold")
    ax.set_title("In-room wall-to-wall distances: real CAD vs SIMULATED 3D-LiDAR cloud (mm). Δ = SIM − CAD. Hallway readings removed.", fontsize=15, color="#d00")
    fig.savefig(f"{out}/COMPARISON_floorplan_cad_vs_simulated_mm.png", dpi=105, bbox_inches="tight"); plt.close(fig)
    fig, ax = plt.subplots(1, 2, figsize=(15, 5.5)); keys = ["≤7 mm", "7–15 mm", "15–30 mm", "30–60 mm", ">60 mm"]
    bins = lambda x: [int((x <= 7).sum()), int(((x > 7) & (x <= 15)).sum()), int(((x > 15) & (x <= 30)).sum()), int(((x > 30) & (x <= 60)).sum()), int((x > 60).sum())]
    xx = np.arange(5); ax[0].bar(xx - .2, bins(np.array(dr)), .4, color="#1f5fe0", label="REAL LiDAR vs CAD (n=%d)" % len(T)); ax[0].bar(xx + .2, bins(a), .4, color="#d00", label="SIMULATED vs CAD (n=%d, illustrative)" % len(T))
    ax[0].set_xticks(xx); ax[0].set_xticklabels(keys); ax[0].legend(); ax[0].set_title("Difference from the real CAD distance (same %d in-room readings)" % len(T))
    ax[1].scatter([t["cad"] for t in T], [t["lidar"] for t in T], c="#1f5fe0", s=25, label="real"); ax[1].scatter([t["cad"] for t in T], [t["sim_measured"] for t in T], c="#d00", s=25, label="simulated"); mx = max(t["cad"] for t in T) * 1.05; ax[1].plot([0, mx], [0, mx], "k--", lw=.8); ax[1].legend(); ax[1].set_xlabel("CAD (mm)"); ax[1].set_ylabel("LiDAR plan (mm)")
    fig.text(0.5, 0.5, "SIMULATED (red) – NOT MEASURED", fontsize=30, color="#d00", alpha=0.10, ha="center", va="center", rotation=15); fig.savefig(f"{out}/COMPARISON_cad_vs_real_vs_simulated.png", dpi=95, bbox_inches="tight"); plt.close(fig)
    print(json.dumps(summ, indent=1))


if __name__ == "__main__":
    root = os.path.dirname(os.path.abspath(__file__)); run(root, os.path.join(root, "..", "poc_demo_files_output"))
