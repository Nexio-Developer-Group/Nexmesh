"""Real CAD wall-to-wall distances vs the SIMULATED 3D-LiDAR plan distances, matched by location/direction. Bins: <=7 mm, 7-15 mm, >15 mm."""
import csv, json, os, sys
import numpy as np
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import cad_distances as CD
from wall_distances import pairs


def run(root, out):
    O = os.path.join(root, "output"); S = os.path.join(out, "simulated_survey_grade")
    cad = CD.run(root, [])                                           # real CAD distances (no DXF rewrite)
    sim = pairs(json.load(open(f"{S}/SIMULATED_plan_faces.json")))
    rows = []
    for c in cad:
        mc = (c["pa"] + c["pb"]) / 2; dc = (c["pb"] - c["pa"]) / np.linalg.norm(c["pb"] - c["pa"]); best = None
        for s in sim:
            ms = (s["pa"] + s["pb"]) / 2; ds = (s["pb"] - s["pa"]) / np.linalg.norm(s["pb"] - s["pa"])
            if abs(dc @ ds) < 0.98: continue
            dd = np.linalg.norm(ms - mc); dv = abs(s["dist"] * 1000 - c["cad"])
            if dd < 0.35 and dv < 200 and (best is None or dd + dv / 1000 < best[0]): best = (dd + dv / 1000, s)
        rows.append(dict(a=c["a"], b=c["b"], cad=c["cad"], cad_src="CAD dimension text" if c["cad_text"] else "CAD geometry", real=c["lidar"], sim=None if best is None else best[1]["dist"] * 1000))
    got = [r for r in rows if r["sim"] is not None]; d = np.array([r["sim"] - r["cad"] for r in got]); a = np.abs(d)
    dr = np.array([r["real"] - r["cad"] for r in rows]); ar = np.abs(dr)
    def bins(x): return dict(le7=int((x <= 7).sum()), b7_15=int(((x > 7) & (x <= 15)).sum()), gt15=int((x > 15).sum()), b15_30=int(((x > 15) & (x <= 30)).sum()), b30_60=int(((x > 30) & (x <= 60)).sum()), gt60=int((x > 60).sum()))
    res = dict(cad_pairs=len(rows), matched_in_simulated=len(got), no_simulated_counterpart=len(rows) - len(got), simulated_vs_cad=dict(n=len(got), bins=bins(a), median_abs_mm=float(np.median(a)), mean_mm=float(d.mean()), p90_abs_mm=float(np.percentile(a, 90)), max_abs_mm=float(a.max())),
               real_lidar_vs_cad=dict(n=len(rows), bins=bins(ar), median_abs_mm=float(np.median(ar)), mean_mm=float(dr.mean()), p90_abs_mm=float(np.percentile(ar, 90))), note="simulated values follow an assumed scanner spec; not measured")
    json.dump(res, open(f"{out}/COMPARISON_cad_vs_simulated_summary.json", "w"), indent=1); print(json.dumps(res, indent=1))
    with open(f"{S}/SIMULATED_vs_REAL_CAD_distance_comparison.csv", "w", newline="") as f:
        w = csv.writer(f); w.writerow(["face_a(real plan)", "face_b", "cad_mm", "cad_source", "real_lidar_mm", "real_lidar_minus_cad", "simulated_mm", "simulated_minus_cad", "bin"])
        for r in rows:
            dv = None if r["sim"] is None else r["sim"] - r["cad"]; b = "no simulated pair" if dv is None else ("<=7 mm" if abs(dv) <= 7 else ("7-15 mm" if abs(dv) <= 15 else ">15 mm"))
            w.writerow([r["a"], r["b"], round(r["cad"]), r["cad_src"], round(r["real"]), round(r["real"] - r["cad"]), "" if r["sim"] is None else round(r["sim"]), "" if dv is None else round(dv), b])
    fig, ax = plt.subplots(1, 2, figsize=(15, 5.5))
    for a_, (name, v, c) in zip(ax[:1], (("", None, None),)): pass
    labs = ["≤7 mm", "7–15 mm", "15–30 mm", "30–60 mm", ">60 mm"]; keys = ["le7", "b7_15", "b15_30", "b30_60", "gt60"]
    x = np.arange(5); ax[0].bar(x - .2, [res["real_lidar_vs_cad"]["bins"][k] for k in keys], .4, color="#1f5fe0", label="REAL LiDAR vs CAD (n=%d)" % len(rows)); ax[0].bar(x + .2, [res["simulated_vs_cad"]["bins"][k] for k in keys], .4, color="#d00", label="SIMULATED vs CAD (n=%d, illustrative)" % len(got))
    ax[0].set_xticks(x); ax[0].set_xticklabels(labs); ax[0].set_ylabel("wall-to-wall distances"); ax[0].legend(); ax[0].set_title("Difference from the real CAD distance")
    ax[1].scatter([r["cad"] for r in rows], [r["real"] for r in rows], c="#1f5fe0", s=25, label="real"); ax[1].scatter([r["cad"] for r in got], [r["sim"] for r in got], c="#d00", s=25, label="simulated"); m = max(r["cad"] for r in rows) * 1.05; ax[1].plot([0, m], [0, m], "k--", lw=.8); ax[1].set_xlabel("CAD (mm)"); ax[1].set_ylabel("LiDAR plan (mm)"); ax[1].legend()
    fig.text(0.5, 0.5, "SIMULATED (red) – NOT MEASURED", fontsize=30, color="#d00", alpha=0.10, ha="center", va="center", rotation=15); fig.savefig(f"{out}/COMPARISON_cad_vs_real_vs_simulated.png", dpi=95, bbox_inches="tight"); plt.close(fig)


if __name__ == "__main__":
    root = os.path.dirname(os.path.abspath(__file__)); run(root, os.path.join(root, "..", "poc_demo_files_output"))
