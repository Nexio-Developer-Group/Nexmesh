"""Per-terrain breakdown for all solutions, on the common 1 Hz grid. Terrain class under the TRUE position comes from the
S5 substitute semantic map (repo SegFormer-B0 on our map imagery; NOT ground-truth land cover): 100 m window; water>10% -> water,
vegetation>40% -> vegetation, building>70% -> building, else mixed. Usage: python terrain.py --runs report/runs.json --out report/terrain.json --fig report/figures/terrain_breakdown.png"""
import argparse, json, os, sys
import numpy as np
import matplotlib; matplotlib.use("Agg")
import matplotlib.pyplot as plt
sys.path.insert(0, os.path.dirname(__file__))
import metrics as M, plots as P
from geo import MapRef

ROOT = P.ROOT
ap = argparse.ArgumentParser(); ap.add_argument("--runs", required=True); ap.add_argument("--out", required=True); ap.add_argument("--fig", required=True)
a = ap.parse_args()
runs = json.load(open(a.runs)); truth = P.load_truth(); ref = MapRef(os.path.join(ROOT, "data/nidar/map_esri_z19.json"))
sd = os.path.join(ROOT, "solutions/s5_swa_pf/work")
lab = np.load(os.path.join(sd, "semmap_1.0.npy")); meta = json.load(open(os.path.join(sd, "semmap_meta.json")))
X0, Y0, gm = meta["x0_px"], meta["y0_px"], meta["gm"]


def cls(i):
    X, Y = ref.latlon_to_px(truth["lat"][i], truth["lon"][i]); r0, c0 = (Y - Y0) * gm, (X - X0) * gm; h = 50
    w = lab[max(0, int(r0 - h)):int(r0 + h), max(0, int(c0 - h)):int(c0 + h)]
    fr = np.bincount(w.ravel(), minlength=7) / max(w.size, 1)
    b, v, wa = fr[1] + fr[5], fr[3], fr[6]
    return "water" if wa > 0.10 else "vegetation" if v > 0.40 else "building" if b > 0.70 else "mixed"


C = np.array([cls(i) for i in range(len(truth["t"]))])
names = ("building", "vegetation", "water", "mixed"); out = {"frames_per_class_1hz": {}}
grid = np.arange(0, truth["t"][-1] + 1e-6, 1.0)
gc = np.array([C[int(np.argmin(abs(truth["t"] - t)))] for t in grid])
for n in names: out["frames_per_class_1hz"][n] = int((gc == n).sum())
for sol, r in runs.items():
    d = r.get("nidar")
    if not d or not os.path.exists(os.path.join(ROOT, d, "estimates.csv")): continue
    est = P.load_est(os.path.join(ROOT, d)); err = P.errors(est, truth)
    res = {}
    for n in names:
        sel = []
        for t in grid[gc == n]:
            i = int(np.argmin(abs(est["t"] - t)))
            if abs(est["t"][i] - t) < 0.06: sel.append(i)
        if not sel: res[n] = None; continue
        e = err[sel]; ok = ~np.isnan(e)
        res[n] = {"frames": len(sel), "fix_rate": float(100 * ok.mean()), "recall_50": float(100 * np.mean(ok & (np.nan_to_num(e, nan=1e9) <= 50))),
                  "median": float(np.median(e[ok])) if ok.any() else None}
    out[sol] = res
json.dump(out, open(a.out, "w"), indent=1)
fig, ax = plt.subplots(figsize=(8, 4)); sols = [s for s in out if s != "frames_per_class_1hz"]; wd = 0.8 / max(len(sols), 1)
for k, s in enumerate(sols):
    ax.bar(np.arange(4) + k * wd, [(out[s][n] or {"recall_50": 0})["recall_50"] for n in names], wd, label=runs[s]["label"], color=P.COL.get(s))
ax.set_xticks(np.arange(4) + 0.4 - wd / 2); ax.set_xticklabels([f"{n}\n({out['frames_per_class_1hz'][n]} fr)" for n in names])
ax.set_ylabel("recall@50 m (% of frames in class)"); ax.legend(fontsize=6); ax.set_title("Terrain breakdown (1 Hz common frames; class from substitute semantic map)")
os.makedirs(os.path.dirname(a.fig), exist_ok=True); fig.savefig(a.fig, dpi=130, bbox_inches="tight")
print(json.dumps(out["frames_per_class_1hz"]))
