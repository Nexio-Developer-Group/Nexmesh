"""Blender model vs REAL LiDAR plan vs SIMULATED plan (from the simulated 3D cloud): (1) clear wall-to-wall distances, (2) wall-face lengths. Simulated = illustrative."""
import csv, json, os, sys
import numpy as np
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wall_distances import pairs
from compare_distances_to_model import ray_hit


def dist_table(faces, msegs):
    rows = []
    for p in pairs([dict(id=f["id"], p0=f["p0"], p1=f["p1"], normal=f["normal"], sigma_m=0.02) for f in faces]):
        n = (p["pb"] - p["pa"]) / np.linalg.norm(p["pb"] - p["pa"]); mid = (p["pa"] + p["pb"]) / 2; h = p["dist"] / 2
        t1 = ray_hit(mid, n, msegs, h + 0.15); t2 = ray_hit(mid, -n, msegs, h + 0.15)
        ok = t1 is not None and t2 is not None and abs(t1 - h) < 0.15 and abs(t2 - h) < 0.15
        rows.append(dict(a=p["a"], b=p["b"], lidar=p["dist"] * 1000, model=(t1 + t2) * 1000 if ok else None))
    for r in rows: r["delta"] = None if r["model"] is None else r["lidar"] - r["model"]
    return rows


def merge_model_runs(msegs, ang_tol=np.deg2rad(2.0), off_tol=0.03, gap=0.10):
    """Merge collinear model section pieces (one per wall object) into continuous wall runs."""
    items = []
    for a, b in msegs:
        e = b - a; l = np.linalg.norm(e)
        if l < 0.05: continue
        d = e / l; d = d if (d[0] > 1e-9 or (abs(d[0]) <= 1e-9 and d[1] > 0)) else -d; n = np.array([-d[1], d[0]])
        items.append((np.arctan2(d[1], d[0]), n @ a, sorted([a @ d, b @ d]), d, n))
    items.sort(key=lambda x: (round(x[0] / ang_tol), x[1]))
    used = [False] * len(items); runs = []
    for i, it in enumerate(items):
        if used[i]: continue
        grp = [it]; used[i] = True; ch = True
        while ch:
            ch = False
            for j, jt in enumerate(items):
                if used[j] or abs(jt[0] - it[0]) > ang_tol: continue
                if abs(jt[1] - it[1]) > off_tol: continue
                lo = min(g[2][0] for g in grp); hi = max(g[2][1] for g in grp)
                if jt[2][0] <= hi + gap and jt[2][1] >= lo - gap: grp.append(jt); used[j] = True; ch = True
        lo = min(g[2][0] for g in grp); hi = max(g[2][1] for g in grp); d, n, r = it[3], it[4], it[1]
        runs.append((n * r + d * lo, n * r + d * hi))
    return runs


def length_table(faces, mruns):
    """Match each face to the model wall run on the same line (parallel, offset <= 15 cm, overlap >= 50 % of the face).
    complete = both face ends within 20 cm of the run ends (a wall seen end to end) -> length error is meaningful.
    Otherwise the face is a partial view: only its coverage fraction is reported."""
    rows = []
    for f in faces:
        p0, p1, n = np.array(f["p0"]), np.array(f["p1"]), np.array(f["normal"]); L = np.linalg.norm(p1 - p0); d = (p1 - p0) / L; ta = sorted([p0 @ d, p1 @ d]); best = None
        for a, b in mruns:
            e = b - a; l = np.linalg.norm(e)
            if l < 0.3 or abs((e / l) @ d) < np.cos(np.deg2rad(3)) or abs(n @ (a - p0)) > 0.15: continue
            tb = sorted([a @ d, b @ d]); ov = min(ta[1], tb[1]) - max(ta[0], tb[0])
            if ov > 0.5 * L and (best is None or ov > best[0]): best = (ov, l, tb)
        if best:
            ov, l, tb = best; complete = abs(ta[0] - tb[0]) <= 0.20 and abs(ta[1] - tb[1]) <= 0.20
            rows.append(dict(id=f["id"], face_mm=L * 1000, model_mm=l * 1000, delta=(L - l) * 1000, coverage=L / l, complete=complete))
    return rows


def stats(v):
    v = np.array(v, float); a = np.abs(v)
    return dict(n=len(v), mean=float(v.mean()), median_abs=float(np.median(a)), p90_abs=float(np.percentile(a, 90)), within_30=float((a <= 30).mean()), within_60=float((a <= 60).mean()))


def run(root, out):
    O = os.path.join(root, "output"); S = os.path.join(out, "simulated_survey_grade"); R = os.path.join(out, "real_measured")
    seg = json.load(open(f"{O}/model_slice.json")); msegs = [(np.array(s["p"][:2]), np.array(s["p"][2:])) for s in seg if s["kind"] in ("Wall", "Column", "Window")]; mruns = merge_model_runs(msegs, gap=0.40); print(len(msegs), "model pieces ->", len(mruns), "continuous wall runs")
    real = json.load(open(f"{O}/floorplan_geometry.json"))["faces"]; sim = json.load(open(f"{S}/SIMULATED_plan_faces.json"))
    res = {}
    for name, faces in (("real", real), ("simulated", sim)):
        d = [r for r in dist_table(faces, msegs) if r["delta"] is not None]; l = length_table(faces, mruns); comp = [r for r in l if r["complete"]]
        res[name] = dict(distance=stats([r["delta"] for r in d]), length_all=dict(n=len(l), median_coverage=float(np.median([r["coverage"] for r in l])), complete_share=float(np.mean([r["complete"] for r in l]))), length_complete=stats([r["delta"] for r in comp]), n_faces=len(faces), rows_d=d, rows_l=l, rows_c=comp)
        with open(f"{(R if name == 'real' else S)}/{'' if name == 'real' else 'SIMULATED_'}wall_distances_and_lengths_vs_model.csv", "w", newline="") as f:
            w = csv.writer(f); w.writerow(["type", "ids", "lidar_mm", "model_mm", "delta_mm"])
            for r in d: w.writerow(["wall_to_wall_distance", r["a"] + "|" + r["b"], round(r["lidar"]), round(r["model"]), round(r["delta"])])
            for r in l: w.writerow(["wall_length_complete" if r["complete"] else "wall_length_partial_view", r["id"], round(r["face_mm"]), round(r["model_mm"]), round(r["delta"]) if r["complete"] else ""])
    json.dump({k: {kk: vv for kk, vv in v.items() if not kk.startswith("rows")} for k, v in res.items()}, open(f"{out}/COMPARISON_walls_summary.json", "w"), indent=1)
    fig, ax = plt.subplots(2, 2, figsize=(17, 11))
    for i, (key, ttl, xl) in enumerate((("distance", "Wall-to-wall distance: LiDAR − model", (-150, 150)), ("length_complete", "Wall length (face fully matching a model wall): LiDAR − model", (-300, 300)))):
        a = ax[0, i]; src = "rows_d" if key == "distance" else "rows_c"
        for name, c in (("real", "#1f5fe0"), ("simulated", "#d00")):
            v = [r["delta"] for r in res[name][src]]; a.hist(np.clip(v, *xl), bins=np.linspace(*xl, 31), alpha=0.5, color=c, label="%s (n=%d, median |Δ| %.0f mm, mean %+.0f)" % (name.upper() + (" (illustrative)" if name == "simulated" else ""), len(v), res[name][key]["median_abs"], res[name][key]["mean"]))
        a.axvline(0, c="k", lw=.8); a.set_title(ttl); a.set_xlabel("mm"); a.legend(fontsize=9)
        b = ax[1, i]
        for name, c in (("real", "#1f5fe0"), ("simulated", "#d00")):
            rr = res[name]["rows_d" if key == "distance" else "rows_c"]; xs = [r["model" if key == "distance" else "model_mm"] for r in rr]; ys = [r["lidar" if key == "distance" else "face_mm"] for r in rr]
            b.scatter(xs, ys, s=14, c=c, alpha=.6, label=name)
        lim = max(b.get_xlim()[1], b.get_ylim()[1]); b.plot([0, lim], [0, lim], "k--", lw=.8); b.set_xlabel("Blender model (mm)"); b.set_ylabel("LiDAR plan (mm)"); b.set_title("model vs LiDAR"); b.legend()
    fig.text(0.5, 0.5, "SIMULATED (red) – NOT MEASURED DATA", fontsize=40, color="#d00", alpha=0.10, ha="center", va="center", rotation=20)
    fig.suptitle("Blender model vs REAL LiDAR plan (blue) and SIMULATED plan from the 3D cloud (red, illustrative)", fontsize=14); fig.savefig(f"{out}/COMPARISON_walls_real_vs_simulated_vs_model.png", dpi=90, bbox_inches="tight"); plt.close(fig)
    for k, v in res.items(): print(k, v["n_faces"], "faces |", {kk: {a: round(b, 2) for a, b in vv.items()} for kk, vv in v.items() if kk in ("distance", "length_all", "length_complete")})


if __name__ == "__main__":
    root = os.path.dirname(os.path.abspath(__file__)); run(root, os.path.join(root, "..", "poc_demo_files_output"))
