"""Machine-readable validation. Every check has value, threshold, rationale, and result in {pass, fail, unverified}.
Checks that cannot be performed with the available data are 'unverified', never silently passed."""
import json, os, re
import numpy as np
from scipy.spatial import cKDTree
import load_lidar as L
import build_map as BM
from geometry import *

CHECKS = []


def chk(cid, group, desc, value, thr, op, rationale, result=None):
    if result is None:
        result = "unverified" if value is None else ("pass" if (value <= thr if op == "<=" else value >= thr) else "fail")
    CHECKS.append(dict(id=cid, group=group, description=desc, value=value, threshold=thr, op=op, rationale=rationale, result=result))


def similarity_scale(pts, tree, mp, iters=40):
    """Trimmed Umeyama similarity fit of the final map onto the model; returns scale (should be 1)."""
    s, R, t = 1.0, np.eye(2), np.zeros(2)
    for _ in range(iters):
        Q = s * pts @ R.T + t; d, j = tree.query(Q, distance_upper_bound=0.4); ok = np.isfinite(d); ok &= d <= np.quantile(d[ok], 0.7)
        A, B = pts[ok], mp[j[ok]]; ma, mb = A.mean(0), B.mean(0); Hh = (A - ma).T @ (B - mb) / len(A)
        U, D, Vt = np.linalg.svd(Hh); Sg = np.eye(2); Sg[1, 1] = np.sign(np.linalg.det(U @ Vt)); R = (U @ Sg @ Vt).T
        va = ((A - ma) ** 2).sum() / len(A); s = float((D * np.diag(Sg)).sum() / va); t = mb - s * R @ ma
    return s


def run(cfg, P):
    CHECKS.clear()
    inv = json.load(open(P(cfg, "inventory.json"))); reg = json.load(open(P(cfg, "registration.json"))); g = json.load(open(P(cfg, "floorplan_geometry.json")))
    od = np.load(P(cfg, "odometry.npz")); an = np.load(P(cfg, "anchored.npz")); so = np.load(P(cfg, "slam_only.npz")); sp = np.load(P(cfg, "scan_poses.npz"))
    seg = json.load(open(P(cfg, "model_slice.json"))); li = inv["lidar"]
    # ---------- input integrity
    chk("I1", "input", "timestamps non-decreasing", li["timestamps_monotonic"], True, ">=", "required for sequential SLAM", result="pass" if li["timestamps_monotonic"] else "fail")
    chk("I2", "input", "malformed records skipped", li["skipped_records"], 0, "<=", "any skipped record is data loss")
    chk("I3", "input", "fraction of scans with <60 points", li["scans_under_60_pts"] / li["scans"], 0.001, "<=", "short fragments (log start) are skipped by the matcher; must be rare")
    chk("I4", "input", "longest timestamp gap (s)", li["max_gap_s"], 1.0, "<=", "gaps >1 s would break constant-velocity prediction; none expected at 13 Hz")
    chk("I5", "input", "sensor extrinsics / angle zero from a calibration file", None, None, ">=", "no calibration file supplied; angle sign validated against model (mirrored map fails), zero direction absorbed in registration yaw", result="unverified")
    chk("I6", "input", "IMU / odometry available for fusion", None, None, ">=", "none in the log (keys t0,t1,a,r); trajectory is LiDAR + model only", result="unverified")
    # ---------- SLAM
    st = od["stats"]; ok = np.isfinite(st[:, 0])
    chk("S1", "slam", "median scan-to-submap inlier fraction (<=15 cm)", float(np.median(st[ok, 1])), 0.9, ">=", "healthy indoor matching; low values indicate poor overlap")
    chk("S2", "slam", "fraction of scans needing correlative recovery", float(np.mean(st[:, 2])), 0.01, "<=", "recovery = tracking nearly lost")
    chk("S3", "slam", "median scan-matching rms (m)", float(np.nanmedian(st[:, 0])), 0.04, "<=", "about 2x the expected range noise of a low-cost 2D LiDAR (unknown model; provisional)")
    X = an["kf_pose"]; jump = np.hypot(*np.diff(X[:, :2], axis=0).T); jump0 = np.hypot(*np.diff(od["kf_pose"][:, :2], axis=0).T)
    chk("S4", "slam", "max keyframe-to-keyframe jump after optimisation / odometry (m)", float(jump.max()), float(max(0.6, jump0.max() * 2)), "<=", "keyframes are <=0.3 m apart; a larger jump = trajectory discontinuity")
    kp = an["kf_pose_odom"]; dpos = []; dyaw = []
    for k in range(len(X) - 1):
        r = relative(kp[k], kp[k + 1]); q = relative(X[k], X[k + 1]); dpos.append(np.hypot(*(r[:2] - q[:2]))); dyaw.append(abs(wrap(r[2] - q[2])))
    chk("S5", "slam", "p95 change of relative keyframe motion odometry -> optimised (m)", float(np.percentile(dpos, 95)), 0.05, "<=", "optimisation should correct drift without distorting local geometry")
    chk("S6", "slam", "p95 change of relative keyframe yaw odometry -> optimised (deg)", float(np.rad2deg(np.percentile(dyaw, 95))), 1.5, "<=", "same, for rotation")
    lo = so["loops"]
    chk("S7", "slam", "verified LiDAR-only loop closures kept", int(len(lo)), 1, ">=", "at least some independent LiDAR-only revisits confirm consistency")
    chk("S8", "slam", "anchoring rounds converged: last-round median inlier fraction", reg["anchoring_rounds"][-1]["inlier_median"], 0.85, ">=", "chunks fit the model section at the end of optimisation")
    chk("S9", "slam", "pose-graph solver status 'converged' (scipy status>0)", int(reg["anchoring_rounds"][-1]["solver_status"] > 0), 1, ">=", "status 0 = max evaluations reached; cost is stable across rounds but not formally converged", result=("pass" if reg["anchoring_rounds"][-1]["solver_status"] > 0 else "fail"))
    # ---------- registration
    off = reg["first_pose_after_anchoring"]["offset_from_sphere_m"]
    chk("R1", "registration", "first estimated pose offset from sphere (m)", off, 0.29, "<=", "sphere radius is 0.29 m; sphere centre is only an approximate start marker")
    ranked = reg["start_candidates_ranked"]; best = ranked[0]
    chk("R2", "registration", "sphere-consistent start is the top-ranked unconstrained global fit", int(best["dist_to_sphere_m"] < 2.0), 1, ">=", "global localisation of the first 12 keyframes without any positional prior", result="pass" if best["dist_to_sphere_m"] < 2.0 else "fail")
    chk("R3", "registration", "margin of top candidate over runner-up (score)", float(ranked[0]["score"] - ranked[1]["score"]), 0.01, ">=", "repeated rooms make start localisation ambiguous; small margin means the sphere is what disambiguates")
    model = np.vstack([(lambda a, b: a + (b - a) * np.linspace(0, 1, max(2, int(np.linalg.norm(b - a) / 0.03)))[:, None])(np.array(s["p"][:2]), np.array(s["p"][2:])) for s in seg if s["kind"] in ("Wall", "Column", "Window", "Door")])
    mt = cKDTree(model)
    zc = np.load(P(cfg, "processed_registered.npz")); pts = zc["xy"].astype(float)[::3]
    d, _ = mt.query(pts, distance_upper_bound=1.0)
    chk("R4", "registration", "fraction of map points within 10 cm of model section", float(np.mean(d < 0.10)), 0.8, ">=", "remaining share is furniture/open doors/as-built differences; model-dependent (anchored)")
    chk("R5", "registration", "median distance map point -> model (m)", float(np.median(d[d < 1.0])), 0.03, "<=", "sub-3 cm needs both a good trajectory and an accurate model")
    chk("R6", "registration", "LiDAR-only map: fraction within 10 cm after best rigid fit", reg["lidar_only_map_rigid_fit"]["frac_within_10cm"], None, ">=", "diagnostic of residual SLAM drift without anchoring (not a pass/fail quantity)", result="unverified")
    sc = similarity_scale(pts[::4], mt, model)
    chk("R7", "registration", "|estimated scale - 1| (similarity fit of map to model)", abs(sc - 1), 0.005, "<=", "model is metric, LiDAR is metric; scale must be consistent. estimated scale=%.5f" % sc)
    # ---------- measurement
    faces = g["faces"]; fo = []
    mseg = [(np.array(s["p"][:2]), np.array(s["p"][2:])) for s in seg if s["kind"] in ("Wall", "Column")]
    mdirs = np.array([(b - a) / max(np.linalg.norm(b - a), 1e-9) for a, b in mseg]); mmid = np.array([(a + b) / 2 for a, b in mseg]); mtree = cKDTree(mmid)
    dev = []
    for f in faces:
        p0, p1 = np.array(f["p0"]), np.array(f["p1"]); dvec = (p1 - p0) / np.linalg.norm(p1 - p0); n = np.array(f["normal"]); mid = (p0 + p1) / 2
        best = np.inf
        for j in mtree.query_ball_point(mid, 1.0):
            if abs(dvec @ mdirs[j]) < np.cos(np.deg2rad(3)): continue
            a, b = mseg[j]; best = min(best, abs(n @ (a - mid)))
        if np.isfinite(best): dev.append(best)
    dev = np.array(dev)
    chk("M1", "measurement", "median perpendicular offset LiDAR face -> nearest parallel model wall face (m)", float(np.median(dev)), 0.03, "<=", "design vs as-built + registration; model-dependent")
    chk("M2", "measurement", "p90 perpendicular offset LiDAR face -> model (m)", float(np.percentile(dev, 90)), 0.06, "<=", "tail dominated by as-built differences and angled walls")
    # rooms vs nominal label sizes (design clear sizes)
    rr = []
    for r in g["rooms"]:
        if r["nominal_mm"] and r["status"] != "model_derived":
            n = sorted([r["nominal_mm"][0] / 1000.0, r["nominal_mm"][1] / 1000.0]); m = sorted(r["min_rect_m"]); rr.append((m[0] - n[0], m[1] - n[1], r["id"], r["observed_perimeter_frac"]))
    chk("M4", "measurement", "median |measured room dimension - nominal label size| (m), observed rooms", float(np.median(np.abs([x for r in rr for x in r[:2]]))) if rr else None, 0.08, "<=",
        "label sizes are design intent; rooms bounded partly by model-derived edges, door lines and furniture bias the min-rectangle; n=%d rooms" % len(rr))
    rect = [r["area_m2"] / (r["min_rect_m"][0] * r["min_rect_m"][1]) for r in g["rooms"] if r["status"] == "observed"]
    chk("M5", "measurement", "room boundary consistency: mean rectangularity (area / min-rect area), observed rooms", float(np.mean(rect)) if rect else None, 0.8, ">=", "most rooms are near-rectangular; very low values flag boundary leaks")
    chk("M6", "measurement", "median face uncertainty sigma (m)", float(np.median([f["sigma_m"] for f in faces])), 0.04, "<=", "sigma = sqrt((rms/sqrt(n_scans))^2 + sigma_registration^2)")
    cov = g["stats"]["model_wall_len_supported_m"] / g["stats"]["model_wall_len_m"]
    chk("M7", "measurement", "fraction of model wall length supported by LiDAR (8 cm)", float(cov), 0.6, ">=", "floor coverage; unsupported wall is labelled model-derived in the plan")
    # duplicates among merged faces
    dup = 0
    for i in range(len(faces)):
        for j in range(i + 1, len(faces)):
            a, b = faces[i], faces[j]
            if abs(np.array(a["normal"]) @ np.array(b["normal"]) - 1) < 1e-3 and abs(np.array(a["normal"]) @ (np.array(b["p0"]) - np.array(a["p0"]))) < 0.015:
                da = np.array([a["normal"][1], -a["normal"][0]]); ta = sorted([np.array(a["p0"]) @ da, np.array(a["p1"]) @ da]); tb = sorted([np.array(b["p0"]) @ da, np.array(b["p1"]) @ da])
                if min(ta[1], tb[1]) - max(ta[0], tb[0]) > 0.2: dup += 1
    chk("M8", "measurement", "overlapping duplicate wall faces after merge", dup, 0, "<=", "repeated estimates of the same wall must be merged")
    # reference dimension annotations from the model (design) are not LiDAR-independent measurements of the building: say so
    chk("M9", "measurement", "comparison with surveyed (as-built) reference dimensions", None, None, ">=", "no total-station / tape reference dimensions were supplied; only model design dimensions are available", result="unverified")
    # ---------- integrity of outputs and of the original model
    out = {}
    try:
        import bpy
        bpy.ops.wm.open_mainfile(filepath=cfg["paths"]["blend"]); orig = {o.name: np.array(o.matrix_world) for o in bpy.data.objects}
        bpy.ops.wm.open_mainfile(filepath=P(cfg, "registered_scene.blend")); new = {o.name: np.array(o.matrix_world) for o in bpy.data.objects}
        same = all(k in new and np.allclose(orig[k], new[k], atol=1e-6) for k in orig)
        colls = sorted(c.name for c in bpy.data.collections)
        need = ["ORIGINAL_BUILDING", "START_REFERENCE", "LIDAR_RAW", "LIDAR_REGISTERED", "TRAJECTORY", "FLOOR_PLAN_GEOMETRY", "REGISTRATION_REFERENCES", "DIAGNOSTICS"]
        chk("O1", "outputs", "all original objects present with unchanged world matrices in output .blend", int(same), 1, ">=", "reopened output and compared against the original file", result="pass" if same else "fail")
        chk("O2", "outputs", "required collections exist in reopened output .blend", int(all(n in colls for n in need)), 1, ">=", "collections found: %s" % colls[:12] + ("..." if len(colls) > 12 else ""), result="pass" if all(n in colls for n in need) else "fail")
    except Exception as e:                                           # pragma: no cover
        chk("O1", "outputs", "reopen output .blend", None, None, ">=", "could not reopen: %r" % e, result="fail")
    ply = BM.read_ply(P(cfg, "registered_processed.ply"))
    chk("O3", "outputs", "registered PLY reloads with finite coordinates", int(len(ply) > 0 and np.isfinite(ply["x"]).all()), 1, ">=", "%d points reloaded" % len(ply), result="pass" if len(ply) > 0 and np.isfinite(ply["x"]).all() else "fail")
    import ezdxf
    dx = ezdxf.readfile(P(cfg, "floorplan.dxf")); n_ent = len(list(dx.modelspace()))
    chk("O4", "outputs", "DXF reopens and holds geometry", int(n_ent > 0), 1, ">=", "%d entities" % n_ent, result="pass" if n_ent > 0 else "fail")
    import xml.dom.minidom as md
    md.parse(P(cfg, "floorplan.svg"))
    chk("O5", "outputs", "SVG parses as XML", 1, 1, ">=", "ok", result="pass")
    summ = {k: sum(1 for c in CHECKS if c["result"] == k) for k in ("pass", "fail", "unverified")}
    rep = dict(summary=summ, scale_estimate=sc, room_vs_nominal=[dict(room=r[2], d_short_m=float(r[0]), d_long_m=float(r[1]), observed_perimeter_frac=float(r[3])) for r in rr],
               checks=CHECKS,
               note="thresholds are provisional engineering judgements (sensor model unknown), each justified in 'rationale'; model-dependent checks are not independent of anchoring")
    json.dump(rep, open(P(cfg, "validation_report.json"), "w"), indent=1, default=float)
    return rep
