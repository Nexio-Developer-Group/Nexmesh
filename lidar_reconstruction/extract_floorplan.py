"""Floor-plan extraction from the registered LiDAR map: Hough wall-face detection with robust refit, wall pairing (thickness),
openings, free-space rooms/corridors, and comparison with model-derived geometry. Observed vs model-derived is tracked per feature."""
import json, logging, re
import numpy as np
from scipy import ndimage as ndi
from scipy.spatial import cKDTree
from shapely.geometry import Polygon, LineString, Point, MultiPolygon
from shapely.ops import unary_union
import contourpy
from geometry import *

log = logging.getLogger("floorplan")


def normals_of(pts, k=60):
    t = cKDTree(pts); _, nb = t.query(pts, k=k)
    Q = pts[nb] - pts[nb].mean(1, keepdims=True); C = np.einsum("nki,nkj->nij", Q, Q) / k
    w, v = np.linalg.eigh(C)
    return v[:, :, 0], 1 - w[:, 0] / np.maximum(w[:, 1], 1e-12)


def detect_faces(pts, scan_id, sensor_xy, tol=0.03, min_len=0.4, min_scans=4, gap=0.15, rho_bin=0.02, max_lines=800):
    """Iterative Hough detection of wall faces. Points need locally linear structure; each face is a TLS-fit segment."""
    n, lin = normals_of(pts)
    ok = lin > 0.6
    P, N, S = pts[ok], n[ok], scan_id[ok]
    sens = sensor_xy[S]
    # orient normals towards the sensor (the observed side of the face)
    flip = np.einsum("ij,ij->i", N, sens - P) < 0
    N = np.where(flip[:, None], -N, N)
    alive = np.ones(len(P), bool); faces = []
    ang = np.arctan2(N[:, 1], N[:, 0])
    for _ in range(max_lines):
        idx = np.flatnonzero(alive)
        if len(idx) < 30:
            break
        ph = (np.round(np.rad2deg(ang[idx])) % 360).astype(int)
        rho = np.einsum("ij,ij->i", P[idx], N[idx])
        rb = np.round(rho / rho_bin).astype(np.int64)
        H = {}
        key = ph * 100000 + (rb + 50000)
        u, cnt = np.unique(key, return_counts=True)
        # smooth over +-1 bin in rho and phi
        cmap = dict(zip(u.tolist(), cnt.tolist()))
        best_k, best_v = None, 0
        for kk, v in zip(u[np.argsort(-cnt)[:50]].tolist(), np.sort(cnt)[::-1][:50].tolist()):
            s = sum(cmap.get(kk + dp * 100000 + dr, 0) for dp in (-1, 0, 1) for dr in (-1, 0, 1))
            if s > best_v:
                best_v, best_k = s, kk
        if best_v < 40:
            break
        ph0 = best_k // 100000; r0 = (best_k % 100000 - 50000) * rho_bin
        th = np.deg2rad(ph0); nn = np.array([np.cos(th), np.sin(th)])
        sel = idx[(np.abs(np.einsum("ij,j->i", P[idx], nn) - r0) < tol * 1.5) & (np.abs(wrap(ang[idx] - th)) < np.deg2rad(6))]
        if len(sel) < 30:
            alive[idx[(np.abs(np.einsum("ij,j->i", P[idx], nn) - r0) < 0.05)]] = False; continue
        # refit by TLS then re-select
        for _ in range(2):
            c = P[sel].mean(0); u_, s_, vt = np.linalg.svd(P[sel] - c, full_matrices=False)
            dvec = vt[0]; nn = np.array([-dvec[1], dvec[0]])
            if nn @ np.array([np.cos(th), np.sin(th)]) < 0:
                nn = -nn
            r0 = nn @ c
            sel = idx[(np.abs(P[idx] @ nn - r0) < tol) & (np.abs(wrap(ang[idx] - np.arctan2(nn[1], nn[0]))) < np.deg2rad(8))]
            if len(sel) < 30:
                break
        if len(sel) < 30:
            alive[idx[(np.abs(P[idx] @ nn - r0) < 0.05)]] = False; continue
        dvec = np.array([nn[1], -nn[0]]); t = P[sel] @ dvec
        order = np.argsort(t); ts = t[order]
        breaks = np.flatnonzero(np.diff(ts) > gap)
        st = np.r_[0, breaks + 1]; en = np.r_[breaks + 1, len(ts)]
        for a, b in zip(st, en):
            ii = sel[order[a:b]]
            if b - a < 20:
                continue
            L_ = ts[b - 1] - ts[a]
            ns = len(np.unique(S[ii]))
            if L_ < min_len or ns < min_scans:
                continue
            c = P[ii].mean(0); u_, s_, vt = np.linalg.svd(P[ii] - c, full_matrices=False)
            d2 = vt[0] if vt[0] @ dvec > 0 else -vt[0]; n2 = np.array([-d2[1], d2[0]])
            if n2 @ nn < 0:
                n2 = -n2
            tt = (P[ii] - c) @ d2; resid = (P[ii] - c) @ n2
            lo, hi = np.percentile(tt, 0.5), np.percentile(tt, 99.5)
            p0 = c + d2 * lo; p1 = c + d2 * hi
            faces.append(dict(p0=p0, p1=p1, normal=n2, n_points=int(b - a), n_scans=int(ns), rms=float(np.sqrt(np.mean(resid ** 2))),
                              length=float(hi - lo)))
        alive[sel] = False
    return faces


def merge_faces(faces, ang_tol=np.deg2rad(2.0), off_tol=0.04, gap=0.12):
    """Merge collinear, same-facing, overlapping/near-touching segments (duplicates from repeated detection)."""
    used = [False] * len(faces); out = []
    for i, f in enumerate(faces):
        if used[i]:
            continue
        grp = [i]; used[i] = True; changed = True
        while changed:
            changed = False
            for j, g in enumerate(faces):
                if used[j]:
                    continue
                if any(_collinear(faces[k], g, ang_tol, off_tol, gap) for k in grp):
                    grp.append(j); used[j] = True; changed = True
        if len(grp) == 1:
            out.append(f); continue
        pts = np.vstack([[faces[k]["p0"], faces[k]["p1"]] for k in grp]); w = np.array([faces[k]["n_points"] for k in grp], float)
        nn = sum(faces[k]["normal"] * faces[k]["n_points"] for k in grp); nn /= np.linalg.norm(nn)
        d = np.array([nn[1], -nn[0]]); r0 = sum((faces[k]["p0"] @ nn) * faces[k]["n_points"] for k in grp) / w.sum()
        t = pts @ d; p0 = d * t.min() + nn * r0; p1 = d * t.max() + nn * r0
        out.append(dict(p0=p0, p1=p1, normal=nn, n_points=int(w.sum()), n_scans=max(faces[k]["n_scans"] for k in grp),
                        rms=float(np.sqrt(sum(faces[k]["rms"] ** 2 * faces[k]["n_points"] for k in grp) / w.sum())), length=float(t.max() - t.min())))
    return out


def extend_to_corners(faces, max_ext=0.25, min_angle_deg=60):
    """Points near a corner have unreliable normals and are rejected, so face ends stop 5-15 cm short of the corner. Extend an end
    to the intersection with a non-parallel face when that intersection lies within max_ext of the end and of that face's extent.
    The extension is recorded (ext0_m / ext1_m) because it is inferred, not directly observed."""
    out = [dict(f, ext0_m=0.0, ext1_m=0.0) for f in faces]
    for i, f in enumerate(out):
        d = (f["p1"] - f["p0"]) / max(np.linalg.norm(f["p1"] - f["p0"]), 1e-9)
        for end in (0, 1):
            e = f["p0"] if end == 0 else f["p1"]; dirn = -d if end == 0 else d
            best = None
            for j, g in enumerate(faces):
                if j == i:
                    continue
                ang = np.rad2deg(np.arccos(min(1.0, abs(f["normal"] @ g["normal"]))))
                if ang < min_angle_deg:
                    continue
                # intersection of line (e + s*dirn) with g's line n_g.x = r_g
                den = g["normal"] @ dirn
                if abs(den) < 1e-6:
                    continue
                s_ = (g["normal"] @ g["p0"] - g["normal"] @ e) / den
                if not (-0.05 <= s_ <= max_ext):
                    continue
                q = e + dirn * s_; dg = np.array([g["normal"][1], -g["normal"][0]]); tg = sorted([g["p0"] @ dg, g["p1"] @ dg])
                if tg[0] - max_ext <= q @ dg <= tg[1] + max_ext and (best is None or abs(s_) < abs(best[0])):
                    best = (s_, q)
            if best is not None:
                if end == 0:
                    f["p0"] = best[1]; f["ext0_m"] = float(max(best[0], 0))
                else:
                    f["p1"] = best[1]; f["ext1_m"] = float(max(best[0], 0))
        f["length"] = float(np.linalg.norm(f["p1"] - f["p0"]))
    return out


def _collinear(a, b, ang_tol, off_tol, gap):
    if np.rad2deg(np.arccos(np.clip(a["normal"] @ b["normal"], -1, 1))) > np.rad2deg(ang_tol):
        return False
    r = a["normal"] @ ((b["p0"] + b["p1"]) / 2) - a["normal"] @ a["p0"]
    if abs(r) > off_tol:
        return False
    d = np.array([a["normal"][1], -a["normal"][0]])
    ta = sorted([a["p0"] @ d, a["p1"] @ d]); tb = sorted([b["p0"] @ d, b["p1"] @ d])
    return tb[0] <= ta[1] + gap and ta[0] <= tb[1] + gap


def pair_faces(faces, tmin=0.06, tmax=0.40, ang_tol=np.deg2rad(3.0)):
    """Pair back-to-back faces (normals pointing away from each other) -> wall thickness and centreline."""
    walls = []
    for i, a in enumerate(faces):
        for j in range(i + 1, len(faces)):
            b = faces[j]
            if np.rad2deg(np.arccos(np.clip(-(a["normal"] @ b["normal"]), -1, 1))) > np.rad2deg(ang_tol):
                continue
            d = np.array([a["normal"][1], -a["normal"][0]])
            # b must lie behind a (opposite its normal): signed offset
            off = (a["normal"] @ ((b["p0"] + b["p1"]) / 2)) - a["normal"] @ a["p0"]
            if not (-tmax <= off <= -tmin):
                continue
            ta = sorted([a["p0"] @ d, a["p1"] @ d]); tb = sorted([b["p0"] @ d, b["p1"] @ d])
            lo, hi = max(ta[0], tb[0]), min(ta[1], tb[1])
            if hi - lo < 0.3:
                continue
            walls.append(dict(a=i, b=j, thickness=float(-off), overlap=float(hi - lo), t0=float(lo), t1=float(hi), d=d, nrm=a["normal"], r_a=float(a["normal"] @ a["p0"])))
    return walls


def seg_coverage(p0, p1, tree, tol=0.06, step=0.05):
    L_ = np.linalg.norm(p1 - p0); n = max(2, int(L_ / step))
    s = p0 + (p1 - p0) * np.linspace(0, 1, n)[:, None]
    d, _ = tree.query(s, distance_upper_bound=tol)
    return float(np.mean(np.isfinite(d))), s, np.isfinite(d)


def run(cfg, P):
    fp = cfg["floorplan"]
    z = np.load(P(cfg, "processed_registered.npz")); sp = np.load(P(cfg, "scan_poses.npz"))["poses"]
    inv = json.load(open(P(cfg, "inventory.json"))); seg = json.load(open(P(cfg, "model_slice.json"))); txt = json.load(open(P(cfg, "model_text.json")))
    anch = np.load(P(cfg, "anchored.npz"))
    xy, sid = z["xy"].astype(float), z["scan_id"]
    pts = voxel_filter(xy, 0.02); sid_v = sid[cKDTree(xy).query(pts)[1]]
    log.info("floorplan: %d points after 2 cm thinning", len(pts))
    faces = merge_faces(detect_faces(pts, sid_v, sp[:, :2], tol=fp["line_inlier_tol_m"], min_len=fp["min_wall_len_m"], min_scans=fp["persistence_min_scans"]))
    faces = extend_to_corners(faces)
    log.info("floorplan: %d wall faces", len(faces))
    sigma_reg = float(np.nanmedian(anch["sigma_xy"]))
    for k, f in enumerate(faces):
        f["id"] = "F%03d" % k
        f["sigma_m"] = float(np.sqrt((f["rms"] / np.sqrt(max(f["n_scans"], 1))) ** 2 + sigma_reg ** 2))
    walls = pair_faces(faces)
    ltree = cKDTree(pts)
    # ---- model geometry (section at LiDAR height) and coverage by LiDAR
    mw = [s for s in seg if s["kind"] in ("Wall", "Column", "Window")]
    model_unobs, model_obs_len, model_tot_len = [], 0.0, 0.0
    for s in mw:
        p0, p1 = np.array(s["p"][:2]), np.array(s["p"][2:])
        cov, samp, hit = seg_coverage(p0, p1, ltree, tol=0.08)
        L_ = np.linalg.norm(p1 - p0); model_tot_len += L_; model_obs_len += L_ * cov
        # split into unobserved runs
        runs, a = [], None
        for i, h in enumerate(hit):
            if not h and a is None: a = i
            if h and a is not None: runs.append((a, i - 1)); a = None
        if a is not None: runs.append((a, len(hit) - 1))
        for a, b in runs:
            if np.linalg.norm(samp[b] - samp[a]) >= 0.15:
                model_unobs.append(dict(p0=samp[a], p1=samp[b], kind=s["kind"], source=s["name"]))
    log.info("model section: %.1f m total, %.1f m (%.0f%%) supported by LiDAR within 8 cm", model_tot_len, model_obs_len, 100 * model_obs_len / model_tot_len)
    # ---- openings: gaps between collinear observed faces (same facing) where the model has a Door
    doors = [s for s in seg if s["kind"] == "Door"]
    dpts = np.array([[(s["p"][0] + s["p"][2]) / 2, (s["p"][1] + s["p"][3]) / 2] for s in doors]) if doors else np.zeros((0, 2))
    dtree = cKDTree(dpts) if len(dpts) else None
    openings = []
    for i in range(len(faces)):
        for j in range(i + 1, len(faces)):
            a, b = faces[i], faces[j]
            if not _collinear_open(a, b):
                continue
            d = np.array([a["normal"][1], -a["normal"][0]])
            ta = sorted([a["p0"] @ d, a["p1"] @ d]); tb = sorted([b["p0"] @ d, b["p1"] @ d])
            g0, g1 = (ta[1], tb[0]) if ta[1] < tb[0] else (tb[1], ta[0])
            w = g1 - g0
            if not (fp["door_gap_min_m"] <= w <= 2.6):
                continue
            r = a["normal"] @ a["p0"]; c = d * (g0 + g1) / 2 + a["normal"] * r
            near_door = bool(dtree is not None and dtree.query(c, distance_upper_bound=0.8)[0] < 0.8)
            openings.append(dict(id="O%03d" % len(openings), center=c, width=float(w), p0=d * g0 + a["normal"] * r, p1=d * g1 + a["normal"] * r,
                                 faces=[a["id"], b["id"]], model_door_nearby=near_door, sigma_m=float(np.hypot(a["sigma_m"], b["sigma_m"]))))
    # ---- rooms from free space
    rooms, corridors = rooms_from_free_space(faces, model_unobs, doors, pts, txt, fp, openings)
    out = dict(frame="Blender world, metres, +Y reference direction", origin=[0, 0], sensor_height_m=inv["sphere"]["world_loc"][2],
               faces=[_ser(f) for f in faces], wall_pairs=[_serw(w, faces) for w in walls], openings=[_ser(o) for o in openings],
               model_derived_unobserved=[_ser(m) for m in model_unobs], rooms=rooms,
               stats=dict(model_wall_len_m=model_tot_len, model_wall_len_supported_m=model_obs_len, sigma_registration_m=sigma_reg,
                          n_faces=len(faces), n_pairs=len(walls), n_openings=len(openings)))
    json.dump(out, open(P(cfg, "floorplan_geometry.json"), "w"), indent=1)
    import export_floorplan, measure_geometry
    measure_geometry.write_tables(cfg, P, out)
    export_floorplan.export_all(cfg, P, out, pts)


def _collinear_open(a, b):
    if np.rad2deg(np.arccos(np.clip(a["normal"] @ b["normal"], -1, 1))) > 2.5:
        return False
    return abs(a["normal"] @ ((b["p0"] + b["p1"]) / 2) - a["normal"] @ a["p0"]) < 0.05


def _ser(o):
    out = {}
    for k, v in o.items():
        out[k] = v.tolist() if isinstance(v, np.ndarray) else (float(v) if isinstance(v, (np.floating,)) else v)
    return out


def _serw(w, faces):
    a, b = faces[w["a"]], faces[w["b"]]
    d, n = w["d"], w["nrm"]; ra, rb = w["r_a"], w["r_a"] - w["thickness"]; rc = (ra + rb) / 2
    return dict(faces=[a["id"], b["id"]], thickness_m=w["thickness"], overlap_m=w["overlap"], centerline=[(d * w["t0"] + n * rc).tolist(), (d * w["t1"] + n * rc).tolist()],
                sigma_thickness_m=float(np.hypot(a["sigma_m"], b["sigma_m"])))


ROOM_NAME = re.compile(r"^(BEDROOM|TOILET|KITCHEN|LIVING|PASSAGE|LOBBY|BALCONY|DRY BAL|LIFT SHAFT|STUDY|UTILITY|STORE|STAIR|SERVICE)", re.I)


def rooms_from_free_space(faces, model_unobs, doors, pts, txt, fp, openings=(), res=0.05):
    """Free-space cells enclosed by observed wall faces + model-derived walls + closed door lines -> room polygons."""
    lo = pts.min(0) - 2.0; hi = pts.max(0) + 2.0
    W, H = int((hi[0] - lo[0]) / res), int((hi[1] - lo[1]) / res)
    occ = np.zeros((H, W), bool); obs_edge = np.zeros((H, W), bool)

    def draw(p0, p1, target, th=1):
        n = max(2, int(np.linalg.norm(p1 - p0) / (res / 2)))
        s = p0 + (p1 - p0) * np.linspace(0, 1, n)[:, None]
        ij = np.floor((s - lo) / res).astype(int)
        for dy in range(-th, th + 1):
            for dx in range(-th, th + 1):
                x = np.clip(ij[:, 0] + dx, 0, W - 1); y = np.clip(ij[:, 1] + dy, 0, H - 1); target[y, x] = True
    ij = np.floor((pts - lo) / res).astype(int); cnt = np.zeros((H, W), np.int32); np.add.at(cnt, (ij[:, 1], ij[:, 0]), 1)
    pt_occ = ndi.binary_dilation(cnt >= 2, iterations=1)       # observed returns (walls, columns, furniture) are barriers
    occ |= pt_occ; obs_edge |= ndi.binary_dilation(cnt >= 2, iterations=2)
    for f in faces:
        draw(f["p0"], f["p1"], occ, 2); draw(f["p0"], f["p1"], obs_edge, 2)
    for o in openings:      # doorways close the room (they are boundaries between spaces, not walls)
        draw(o["p0"], o["p1"], occ, 2)
    for m in model_unobs:
        draw(m["p0"], m["p1"], occ, 2)
    for s in doors:
        draw(np.array(s["p"][:2]), np.array(s["p"][2:]), occ, 2)
    # also close the 0.1 m cells that contain LiDAR points but no fitted face? (furniture/noise are deliberately ignored)
    free = ~occ
    lab, n = ndi.label(free)
    border = set(np.unique(np.r_[lab[0], lab[-1], lab[:, 0], lab[:, -1]]).tolist())
    rooms = []
    labels = [t for t in txt if t["coll"] == "07_Labels"]
    size_re = re.compile(r"^(\d{3,5})X(\d{3,5})$")
    for k in range(1, n + 1):
        if k in border:
            continue
        m = lab == k; area = m.sum() * res * res
        if area < 1.5 or area > 250:
            continue
        gen = contourpy.contour_generator(z=np.pad(m, 1).astype(float))
        lines = gen.lines(0.5)
        if not lines:
            continue
        polys = [Polygon((np.array(l) - 1.0 + 0.5) * res + lo) for l in lines if len(l) > 6]
        # barrier cells were dilated by one 5 cm cell: free-space boundary lies on average 1.5 cells inside the wall face
        poly = max(polys, key=lambda p: p.area).buffer(1.5 * res, join_style=2).simplify(0.02)
        if poly.geom_type != "Polygon":
            continue
        if poly.area < 1.0:
            continue
        obs_frac = float(ndi.binary_dilation(m, iterations=2)[obs_edge].sum() / max(obs_edge[ndi.binary_dilation(m, iterations=2)].sum(), 1)) if False else None
        # perimeter coverage by observed faces
        per = poly.exterior; samp = [per.interpolate(t, normalized=True) for t in np.linspace(0, 1, max(20, int(per.length / 0.1)))]
        ij = np.floor((np.array([[p.x, p.y] for p in samp]) - lo) / res).astype(int)
        ij[:, 0] = np.clip(ij[:, 0], 0, W - 1); ij[:, 1] = np.clip(ij[:, 1], 0, H - 1)
        cov = float(obs_edge[ij[:, 1], ij[:, 0]].mean())
        mrr = poly.minimum_rotated_rectangle; xs, ys = mrr.exterior.coords.xy
        e = sorted([np.hypot(xs[1] - xs[0], ys[1] - ys[0]), np.hypot(xs[2] - xs[1], ys[2] - ys[1])])
        inside = [t for t in labels if poly.contains(Point(t["xy"]))]
        named = [t for t in inside if ROOM_NAME.match(t["body"])]
        name_t = named[0] if named else None
        name = name_t["body"] if name_t else None
        sizes = [t for t in inside if size_re.match(t["body"])]
        if name_t and sizes:
            st = min(sizes, key=lambda t: np.hypot(*(np.array(t["xy"]) - np.array(name_t["xy"]))))
            nominal = (int(size_re.match(st["body"]).group(1)), int(size_re.match(st["body"]).group(2)))
        else:
            nominal = None
        rooms.append(dict(id="R%03d" % len(rooms), label=name, nominal_mm=list(nominal) if nominal else None, polygon=[list(map(float, c)) for c in poly.exterior.coords],
                          area_m2=float(poly.area), min_rect_m=[float(e[0]), float(e[1])], perimeter_m=float(per.length), observed_perimeter_frac=cov,
                          status=("observed" if cov >= 0.7 else "partially_observed" if cov >= 0.3 else "model_derived"),
                          sigma_m=float(max(0.03, 0.05 * (1 - cov) + 0.02))))
    return rooms, []
