"""Extra in-room readings from the CAD itself: facing CAD wall-face runs -> matched to the LiDAR faces lying on the same walls (so they exist in the point cloud).
CAD distance = geometric distance between the two CAD wall faces. Used to add readings where the LiDAR-only pairing finds none (e.g. the bottom flats)."""
import json, os, sys
import numpy as np
from scipy.spatial import cKDTree
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import cad_extract, cad_distances as CD


def cad_runs(root):
    O = os.path.join(root, "output"); S, _, _ = cad_extract.extract(os.path.join(root, "data", "cad_plan.pdf"), all_colors=True); r = json.load(open(f"{O}/cad_registration.json")); R = np.array(r["R"]); t = np.array(r["t"]); s = r["scale_m_per_pt"]
    A, B = s * S[:, :2] @ R.T + t, s * S[:, 2:] @ R.T + t; RA, RB = CD.merge_axis_runs(A, B)
    mp = []
    for q in json.load(open(f"{O}/model_slice.json")):
        if q["kind"] in ("Wall", "Column", "Window"):
            a, b = np.array(q["p"][:2]), np.array(q["p"][2:]); k = max(2, int(np.linalg.norm(b - a) / 0.03)); mp.append(a + (b - a) * np.linspace(0, 1, k)[:, None])
    mt = cKDTree(np.vstack(mp)); keep = []
    for a, b in zip(RA, RB):
        k = max(3, int(np.linalg.norm(b - a) / 0.1)); keep.append(np.mean(mt.query(a + (b - a) * np.linspace(0, 1, k)[:, None])[0] < 0.06) >= 0.5)
    keep = np.array(keep); RA, RB = RA[keep], RB[keep]
    runs = []        # (axis, c, lo, hi): axis 'h' -> y=c, x in [lo,hi]; 'v' -> x=c, y in [lo,hi]
    for a, b in zip(RA, RB):
        if abs(a[1] - b[1]) < 1e-6: runs.append(("h", a[1], min(a[0], b[0]), max(a[0], b[0])))
        else: runs.append(("v", a[0], min(a[1], b[1]), max(a[1], b[1])))
    return runs


def candidates(root, region=lambda x, y: True, min_overlap=1.0, dmin=0.8, dmax=8.0, tol=0.20):
    O = os.path.join(root, "output"); faces = json.load(open(f"{O}/floorplan_geometry.json"))["faces"]; runs = cad_runs(root); out = []
    def face_axis(f):
        p0, p1, n = np.array(f["p0"]), np.array(f["p1"]), np.array(f["normal"])
        if abs(n[1]) > 0.98: return "h", (p0[1] + p1[1]) / 2, sorted([p0[0], p1[0]]), n
        if abs(n[0]) > 0.98: return "v", (p0[0] + p1[0]) / 2, sorted([p0[1], p1[1]]), n
        return None
    FA = [(f, face_axis(f)) for f in faces]; FA = [(f, a) for f, a in FA if a]
    for ax in ("h", "v"):
        R_ = sorted([r for r in runs if r[0] == ax], key=lambda r: r[1])
        for i, r1 in enumerate(R_):
            for r2 in R_[i + 1:]:
                d = r2[1] - r1[1]
                if d < dmin: continue
                if d > dmax: break
                lo, hi = max(r1[2], r2[2]), min(r1[3], r2[3])
                if hi - lo < min_overlap: continue
                cmid = (lo + hi) / 2
                if any(r[1] > r1[1] + 0.02 and r[1] < r2[1] - 0.02 and r[2] <= cmid <= r[3] for r in R_): continue          # another wall face in between
                m = (cmid, (r1[1] + r2[1]) / 2) if ax == "h" else ((r1[1] + r2[1]) / 2, cmid)
                if not region(*m): continue
                def best(run, want):                      # LiDAR face on this CAD run, facing the free side (want = +1 / -1 along the axis)
                    b = None
                    for f, (fax, c, ext, n) in FA:
                        if fax != ax or abs(c - run[1]) > 0.12 or (n[1] if ax == "h" else n[0]) * want < 0.9: continue
                        ov = min(ext[1], hi) - max(ext[0], lo)
                        if ov >= 0.15 and (b is None or ov > b[0]): b = (ov, f)
                    return b
                fa, fb = best(r1, +1), best(r2, -1)
                if not fa or not fb: continue
                fa, fb = fa[1], fb[1]; na = np.array(fa["normal"]); da = np.array([na[1], -na[0]])
                ta = sorted([np.array(fa["p0"]) @ da, np.array(fa["p1"]) @ da]); tb = sorted([np.array(fb["p0"]) @ da, np.array(fb["p1"]) @ da]); l2, h2 = max(ta[0], tb[0]), min(ta[1], tb[1])
                # line-to-line distance (faces need not overlap each other: both lie on the facing CAD walls); reading drawn at the CAD overlap centre
                ma, mb = (np.array(fa["p0"]) + np.array(fa["p1"])) / 2, (np.array(fb["p0"]) + np.array(fb["p1"])) / 2; off = na @ (mb - ma)
                if abs(off - d) > tol or off <= 0: continue
                tm = (l2 + h2) / 2 if h2 - l2 >= 0.3 else (cmid * (da[0] if ax == "h" else da[1]) if False else None)
                if tm is None:
                    ref = np.array([cmid, 0.0]) if ax == "h" else np.array([0.0, cmid]); tm = ref @ da
                A_ = ma + da * (tm - ma @ da)
                out.append(dict(a=fa["id"], b=fb["id"], cad=d * 1000, pa=A_, pb=A_ + na * off, dist=off, mid=m, axis=ax, overlap=max(h2 - l2, 0.0)))
    return out


if __name__ == "__main__":
    root = os.path.dirname(os.path.abspath(__file__)); c = candidates(root, region=lambda x, y: y < -4.5)
    print(len(c), "CAD-based candidate pairs with LiDAR faces in the bottom flats (y < -4.5)")
    for q in sorted(c, key=lambda q: (q["mid"][0], q["mid"][1])): print(q["a"], q["b"], q["axis"], "cad %d lidar %d" % (round(q["cad"]), round(q["dist"] * 1000)), np.round(q["mid"], 1).tolist())
