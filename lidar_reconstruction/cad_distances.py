"""Real CAD wall-to-wall distances for the LiDAR comparison pairs, and real_cad_distance_btw_walls.dxf (plan drawing + only those dimensions).
Distance = ray-cast on the architect's CAD wall linework (registered to the Blender frame); confirmed (and replaced) by the CAD's own dimension
string when one lies near the pair and is within 40 mm of the geometric value."""
import csv, json, os, re, sys
import numpy as np
import ezdxf
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import cad_extract
from wall_distances import pairs


def merge_axis_runs(A, B, tol=0.02, gap=0.08, minlen=0.3):
    """CAD linework is dashed/fragmented: merge axis-aligned pieces on the same line into continuous runs (angled pieces are ignored)."""
    runs = []
    for horiz in (True, False):
        d = np.abs(B - A); sel = (d[:, 1] < 0.004) & (d[:, 0] > 1e-4) if horiz else (d[:, 0] < 0.004) & (d[:, 1] > 1e-4)
        a, b = A[sel], B[sel]; fix = np.round(((a[:, 1] + b[:, 1]) / 2 if horiz else (a[:, 0] + b[:, 0]) / 2) / tol).astype(int)
        lo = np.minimum(a[:, 0], b[:, 0]) if horiz else np.minimum(a[:, 1], b[:, 1]); hi = np.maximum(a[:, 0], b[:, 0]) if horiz else np.maximum(a[:, 1], b[:, 1])
        for k in np.unique(fix):
            m = np.flatnonzero(fix == k); o = m[np.argsort(lo[m])]; cur = None
            for i in o:
                if cur is None: cur = [lo[i], hi[i]]
                elif lo[i] <= cur[1] + gap: cur[1] = max(cur[1], hi[i])
                else:
                    runs.append((k * tol, cur[0], cur[1], horiz)); cur = [lo[i], hi[i]]
            if cur: runs.append((k * tol, cur[0], cur[1], horiz))
    R_ = [(np.array([l0, c]) if h else np.array([c, l0]), np.array([l1, c]) if h else np.array([c, l1])) for c, l0, l1, h in runs if l1 - l0 >= minlen]
    return np.array([r[0] for r in R_]), np.array([r[1] for r in R_])


def ray_hits(o, d, A, B, maxd):
    """Nearest hit along o + t d (0<t<maxd) against segments A->B that are ~perpendicular to d. Vectorised."""
    e = B - A; l = np.hypot(e[:, 0], e[:, 1]); ok = l > 0.05
    ed = e / np.maximum(l, 1e-9)[:, None]; ok &= np.abs(ed @ d) < 0.2
    den = d[0] * e[:, 1] - d[1] * e[:, 0]; ok &= np.abs(den) > 1e-9
    w = A - o; t = (w[:, 0] * e[:, 1] - w[:, 1] * e[:, 0]) / np.where(ok, den, 1); u = (w[:, 0] * d[1] - w[:, 1] * d[0]) / np.where(ok, den, 1)
    ok &= (t > 0) & (t < maxd) & (u >= 0) & (u <= 1)
    return float(t[ok].min()) if ok.any() else None


def run(root, out_dirs):
    O = os.path.join(root, "output")
    S, words, size = cad_extract.extract(os.path.join(root, "data", "cad_plan.pdf"), all_colors=True); r = json.load(open(f"{O}/cad_registration.json")); R = np.array(r["R"]); t = np.array(r["t"]); s = r["scale_m_per_pt"]
    tf = lambda P: s * np.asarray(P) @ R.T + t
    A, B = tf(S[:, :2]), tf(S[:, 2:]); RA, RB = merge_axis_runs(A, B); print(len(A), "CAD pieces ->", len(RA), "continuous axis-aligned runs")
    # keep only CAD runs that lie on a real wall face (coincide with the registered Blender wall/column section within 6 cm): drops stair hatching, door swings, dimension lines
    from scipy.spatial import cKDTree
    mp = []
    for q in json.load(open(f"{O}/model_slice.json")):
        if q["kind"] in ("Wall", "Column", "Window"):
            a_, b_ = np.array(q["p"][:2]), np.array(q["p"][2:]); k_ = max(2, int(np.linalg.norm(b_ - a_) / 0.03)); mp.append(a_ + (b_ - a_) * np.linspace(0, 1, k_)[:, None])
    mt = cKDTree(np.vstack(mp)); keep = []
    for a_, b_ in zip(RA, RB):
        k_ = max(3, int(np.linalg.norm(b_ - a_) / 0.1)); pts_ = a_ + (b_ - a_) * np.linspace(0, 1, k_)[:, None]
        keep.append(np.mean(mt.query(pts_)[0] < 0.06) >= 0.5)
    keep = np.array(keep); RA, RB = RA[keep], RB[keep]; print(len(RA), "runs lie on registered wall faces")
    nums = []
    for w in words:
        txt = w["text"] if w["upright"] else w["text"][::-1]
        if re.fullmatch(r"\d{3,5}", txt) and 300 <= int(txt) <= 12000: nums.append((int(txt), tf([[(w["x0"] + w["x1"]) / 2, w["y"]]])[0]))
    print("CAD dimension strings:", len(nums))
    faces = json.load(open(f"{O}/floorplan_geometry.json"))["faces"]
    rows = []
    for p in pairs([dict(id=f["id"], p0=f["p0"], p1=f["p1"], normal=f["normal"], sigma_m=f["sigma_m"]) for f in faces]):
        n = (p["pb"] - p["pa"]) / np.linalg.norm(p["pb"] - p["pa"]); mid = (p["pa"] + p["pb"]) / 2; h = p["dist"] / 2
        t1 = ray_hits(mid, n, RA, RB, h + 0.15); t2 = ray_hits(mid, -n, RA, RB, h + 0.15)
        if t1 is None or t2 is None or abs(t1 - h) > 0.15 or abs(t2 - h) > 0.15: continue
        geo = (t1 + t2) * 1000; best = None
        for val, pos in nums:
            dd = np.linalg.norm(pos - mid)
            if dd < 2.0 and abs(val - geo) <= 40 and (best is None or abs(val - geo) < abs(best - geo)): best = val
        rows.append(dict(a=p["a"], b=p["b"], pa=p["pa"], pb=p["pb"], lidar=p["dist"] * 1000, cad_geo=geo, cad_text=best, cad=float(best) if best else geo))
    print("pairs with a CAD distance: %d (text-confirmed %d)" % (len(rows), sum(r_["cad_text"] is not None for r_ in rows)))
    # ---- CSV
    with open(f"{O}/real_cad_distance_btw_walls.csv", "w", newline="") as f:
        w = csv.writer(f); w.writerow(["face_a", "face_b", "cad_distance_mm", "cad_source", "cad_geometry_mm", "lidar_mm", "lidar_minus_cad_mm"])
        for q in rows: w.writerow([q["a"], q["b"], round(q["cad"]), "CAD dimension text" if q["cad_text"] else "CAD geometry", round(q["cad_geo"]), round(q["lidar"]), round(q["lidar"] - q["cad"])])
    # ---- DXF: plan drawing + only the comparison dimensions
    doc = ezdxf.new("R2018", setup=True); doc.units = ezdxf.units.M; msp = doc.modelspace()
    doc.layers.add("CAD_PLAN_WALLS", color=8); doc.layers.add("CAD_DIM_WALL_TO_WALL", color=1); doc.layers.add("CAD_DIM_TEXT", color=3)
    Sp, _, _ = cad_extract.extract(os.path.join(root, "data", "cad_plan.pdf"))          # plan drawing: wall-linework colours only (no hatch clutter)
    Ap, Bp = tf(Sp[:, :2]), tf(Sp[:, 2:])
    for a, b in zip(Ap, Bp):
        if np.hypot(*(b - a)) < 40: msp.add_line(tuple(a), tuple(b), dxfattribs={"layer": "CAD_PLAN_WALLS"})
    for q in rows:
        pa, pb = q["pa"], q["pb"]; msp.add_line(tuple(pa), tuple(pb), dxfattribs={"layer": "CAD_DIM_WALL_TO_WALL"})
        v = (pb - pa) / np.linalg.norm(pb - pa); nn = np.array([-v[1], v[0]])
        for pt in (pa, pb): msp.add_line(tuple(pt - nn * 0.08), tuple(pt + nn * 0.08), dxfattribs={"layer": "CAD_DIM_WALL_TO_WALL"})
        ang = np.degrees(np.arctan2(v[1], v[0])); ang = ang if -90 < ang <= 90 else ang + 180; mid = (pa + pb) / 2 + nn * 0.10
        msp.add_text("%d" % round(q["cad"]), height=0.11, rotation=ang, dxfattribs={"layer": "CAD_DIM_TEXT"}).set_placement(tuple(mid), align=ezdxf.enums.TextEntityAlignment.MIDDLE_CENTER)
    for d in out_dirs:
        os.makedirs(d, exist_ok=True); doc.saveas(os.path.join(d, "real_cad_distance_btw_walls.dxf"))
    v = np.array([q["lidar"] - q["cad"] for q in rows]); a = np.abs(v)
    print("LiDAR - CAD: n=%d mean %+.0f median|d| %.0f p90|d| %.0f mm; within 30 mm %.0f%%" % (len(v), v.mean(), np.median(a), np.percentile(a, 90), 100 * (a <= 30).mean()))
    return rows


if __name__ == "__main__":
    root = os.path.dirname(os.path.abspath(__file__)); top = os.path.join(root, "..")
    run(root, [os.path.join(root, "output"), os.path.join(top, "original_output"), os.path.join(top, "poc_demo_files_output", "real_measured")])
