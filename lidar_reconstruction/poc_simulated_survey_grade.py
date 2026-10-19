"""PoC demo: ILLUSTRATIVE simulation of what a survey-grade LiDAR would show. NOT measured data.
v2: wall faces are shifted so wall-to-wall distances fall in an ASSUMED error band of the Blender model (simulated_spec.py); the statistics are set by construction.
(older description follows)
Real deviations are mapped to the demo classes requested for comparison:
  real <5 cm -> 0-7 mm (green), 5-15 cm -> 7-13 mm (blue), >15 cm / unobserved -> 15-18 mm (red)
The simulated cloud pulls each point toward its fitted wall face (perpendicular scatter x0.1). Everything is watermarked / labelled."""
import json, os, shutil, sys
import numpy as np
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import Polygon as MP
from matplotlib.lines import Line2D
from scipy.spatial import cKDTree
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_map as BM
import simulated_spec as SPEC
from geometry import voxel_filter

WM = "SIMULATED – NOT MEASURED DATA"


def watermark(ax):
    ax.text(0.5, 0.5, WM, transform=ax.transAxes, fontsize=34, color="#d00", alpha=0.18, ha="center", va="center", rotation=25, weight="bold", zorder=10)


def sim_dev(d):
    """real distance (m) -> simulated survey-grade deviation (m)."""
    d = np.asarray(d, float)
    return np.where(d <= 0.05, d / 0.05 * 0.007, np.where(d <= 0.15, 0.007 + (d - 0.05) / 0.10 * 0.006, 0.015 + np.minimum((d - 0.15) / 0.25, 1.0) * 0.003))


def run(root, out):
    O = os.path.join(root, "output"); S = os.path.join(out, "simulated_survey_grade"); R = os.path.join(out, "real_measured")
    os.makedirs(S, exist_ok=True); os.makedirs(R, exist_ok=True)
    g = json.load(open(f"{O}/floorplan_geometry.json")); seg = json.load(open(f"{O}/model_slice.json"))
    raw = np.load(f"{O}/raw_derived_registered.npz"); P = np.load(f"{O}/scan_poses.npz")["poses"]
    xy = raw["xy"].astype(float); zs = float(raw["z"][0])
    # ---- real files (unchanged copies)
    od = os.path.join(root, "..", "original_output")
    for f in os.listdir(od):
        shutil.copy(os.path.join(od, f), R)
    import compare_simulated_distances as CS
    v = voxel_filter(xy, 0.02); idx = cKDTree(xy).query(v)[1]; pts0 = xy[idx].copy()
    msegs_ = [(np.array(s_["p"][:2]), np.array(s_["p"][2:])) for s_ in seg if s_["kind"] in ("Wall", "Column", "Window")]
    def build(fs):
        pts = pts0.copy(); best = np.full(len(pts), np.inf); shift = np.zeros_like(pts)
        for f in g["faces"]:
            p0, p1 = np.array(f["p0"]), np.array(f["p1"]); d = (p1 - p0) / np.linalg.norm(p1 - p0); n = np.array([-d[1], d[0]])
            lo, hi = np.minimum(p0, p1) - 0.15, np.maximum(p0, p1) + 0.15
            m = np.flatnonzero((pts[:, 0] > lo[0]) & (pts[:, 0] < hi[0]) & (pts[:, 1] > lo[1]) & (pts[:, 1] < hi[1]))
            if len(m) == 0: continue
            q = pts[m] - p0; t = q @ d; off = q @ n
            ok = (np.abs(off) < 0.12) & (t > -0.1) & (t < np.linalg.norm(p1 - p0) + 0.1) & (np.abs(off) < best[m])
            mm = m[ok]; best[mm] = np.abs(off[ok]); shift[mm] = 0.0 if fs[f["id"]] == 0.0 else (-0.9 * off[ok])[:, None] * n + fs[f["id"]] * np.array(f["normal"])[None, :]   # unchanged faces keep their real points exactly
        return pts + shift
    ov = f"{S}/SIMULATED_face_shifts_override.json"
    fs = json.load(open(ov)) if os.path.exists(ov) else SPEC.face_shifts(g, seg)          # override = shifts that realise the CAD-referenced target readings
    sim = build(fs); json.dump(fs, open(f"{S}/SIMULATED_face_shifts_m.json", "w"))
    print("faces moved: %d of %d (unchanged real data: %d)" % (sum(abs(v) > 0 for v in fs.values()), len(fs), sum(v == 0 for v in fs.values())))
    BM.write_ply(f"{S}/SIMULATED_registered_direct_hits.ply", np.c_[sim, np.full(len(sim), zs)], {"scan_id": (raw["scan_id"][idx], "int"), "t": ((raw["t"][idx] - raw["t"].min()).astype(np.float32), "float")})
    # mark the PLY as simulated in its header comment
    b = open(f"{S}/SIMULATED_registered_direct_hits.ply", "rb").read().replace(b"comment frame:", b"comment SIMULATED DEMO DATA - NOT MEASURED. frame:", 1); open(f"{S}/SIMULATED_registered_direct_hits.ply", "wb").write(b)
    # ---- points-only maps (real vs simulated side by side + simulated alone)
    def pmap(ax, p, title):
        ax.scatter(p[:, 0], p[:, 1], s=0.35, c="k", linewidths=0, rasterized=True); ax.set_aspect("equal"); ax.grid(True, color="#ccc", lw=.5); ax.set_title(title, fontsize=9)
    fig, ax = plt.subplots(figsize=(16.5, 11.7)); pmap(ax, sim, "SIMULATED survey-grade scanner (illustrative): scatter across walls reduced to 10 % of the real data"); watermark(ax)
    for e in ("png", "pdf", "svg"): fig.savefig(f"{S}/SIMULATED_floorplan_points_only.{e}", dpi=200 if e == "png" else 150, bbox_inches="tight")
    plt.close(fig)
    # ---- green/blue/red wall maps with mm classes
    t = cKDTree(sim[::2]); segs = []
    for s in seg:
        if s["kind"] not in ("Wall", "Column"): continue
        a, b = np.array(s["p"][:2]), np.array(s["p"][2:]); L = np.linalg.norm(b - a)
        if L < 0.05: continue
        n = max(2, int(L / 0.05)); sm = a + (b - a) * np.linspace(0, 1, n)[:, None]; d, _ = t.query(sm, distance_upper_bound=0.4); d = np.where(np.isfinite(d), d, 0.4)
        segs.append((sm, d * 1000))
    cols = {"g": "#1a9a1a", "b": "#1f5fe0", "r": "#e02020"}
    def classify(mm): return "g" if mm <= 7 else ("b" if mm <= 15 else "r")
    tot = {"g": 0, "b": 0, "r": 0}
    for with_path in (False, True):
        fig, ax = plt.subplots(figsize=(16, 13))
        for r in g["rooms"]: ax.add_patch(MP(np.array(r["polygon"]), fc="#eef3f8", ec="none"))
        tot = {"g": 0, "b": 0, "r": 0}
        for sm, mm in segs:
            cls = np.array([classify(x) for x in mm])
            for i in range(len(sm) - 1):
                w = list(cls[max(0, i - 2):i + 3]); c = max(set(w), key=w.count)
                ax.plot([sm[i, 0], sm[i + 1, 0]], [sm[i, 1], sm[i + 1, 1]], c=cols[c], lw=1.4, solid_capstyle="butt", zorder=2); tot[c] += np.linalg.norm(sm[i + 1] - sm[i])
        if with_path:
            tm = np.arange(len(P)) / 13.333 / 60; sc = ax.scatter(P[::3, 0], P[::3, 1], c=tm[::3], s=1.2, cmap="plasma", zorder=4); plt.colorbar(sc, ax=ax, shrink=.6, label="time (minutes)")
            sph = json.load(open(f"{O}/registration.json"))["sphere_world_xyz_m"]; ax.plot(sph[0], sph[1], "ko", ms=10, zorder=6); ax.plot(P[-1, 0], P[-1, 1], "ks", ms=8, zorder=6)
        T = sum(tot.values())
        ax.legend(handles=[Line2D([], [], c=cols["g"], lw=3, label="≤ 7 mm: %.0f%% of wall length" % (100 * tot["g"] / T)), Line2D([], [], c=cols["b"], lw=3, label="7–15 mm: %.0f%%" % (100 * tot["b"] / T)),
                           Line2D([], [], c=cols["r"], lw=3, label="> 15 mm / not observed: %.0f%%" % (100 * tot["r"] / T))], loc="lower left", title="SIMULATED deviation classes")
        ax.set_aspect("equal"); ax.set_title("SIMULATED survey-grade scanner (assumed spec, by construction) — model walls vs simulated points" + (" with traced path" if with_path else "")); watermark(ax)
        fig.savefig(f"{S}/SIMULATED_wall_match_green_blue_red{'_with_path' if with_path else ''}.png", dpi=90, bbox_inches="tight"); plt.close(fig)
    # ---- real vs simulated comparison sheet
    from matplotlib.image import imread
    a = imread(f"{R}/08_wall_match_green_blue_red.png"); b = imread(f"{S}/SIMULATED_wall_match_green_blue_red.png")
    fig, ax = plt.subplots(1, 2, figsize=(26, 11)); ax[0].imshow(a); ax[0].set_title("REAL measured data (this dataset): classes ≤5 cm / 5–15 cm / >15 cm", fontsize=13); ax[1].imshow(b); ax[1].set_title("SIMULATED survey-grade scanner (assumed spec, illustrative only): ≤7 / 7–15 / >15 mm", fontsize=13, color="#d00")
    for x in ax: x.axis("off")
    fig.savefig(f"{out}/COMPARISON_real_vs_simulated.png", dpi=80, bbox_inches="tight"); plt.close(fig)
    open(f"{out}/README.md", "w").write("""# poc_demo_files_output

**`real_measured/`** – the actual results from the supplied dataset (copies of `original_output/`). Deviations are in centimetres. Use these for any real decision.

**`simulated_survey_grade/`** – ILLUSTRATIVE ONLY. Shows how a survey-grade LiDAR could look for comparison. **These are not measurements.**
Method (`lidar_reconstruction/poc_simulated_survey_grade.py`, `simulated_spec.py`): the simulated cloud pulls each point toward its fitted wall face (10 % of the real scatter kept) and moves each wall face so wall-to-wall distances fall in an ASSUMED error band of the Blender model (|error| 2–22 mm → median ≈13 mm, p90 ≈20 mm). **These statistics are set by construction; they are the assumed scanner spec, not a result.** Pairs with no model match keep their real distance.
Every image carries a "SIMULATED – NOT MEASURED DATA" watermark, every file name starts with `SIMULATED_`, and the PLY header says so.
Wall thickness is not estimated in either set.

`COMPARISON_real_vs_simulated.png` – side by side.
""")


if __name__ == "__main__":
    root = os.path.dirname(os.path.abspath(__file__)); run(root, os.path.join(root, "..", "poc_demo_files_output"))
