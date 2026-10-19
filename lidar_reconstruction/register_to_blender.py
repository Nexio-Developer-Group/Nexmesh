"""Register the LiDAR trajectory/map to the Blender model: sphere-seeded global localisation, model-anchored pose graph,
rigid-fit diagnostics for the unanchored map, and the registration report."""
import logging
import numpy as np
from scipy.signal import fftconvolve
from scipy.ndimage import gaussian_filter, maximum_filter
from scipy.spatial import cKDTree
from geometry import *
import global_reg as GR
import graph_solver as GS
import model_anchor as A

log = logging.getLogger("register")


def locate_start(first_chunk, model_pts, sphere_xy, res=0.1, min_score=0.35):
    """Global search (all rotations, all translations) for where the first chunk of scans fits the model.
    Returns ranked distinct candidates [(score, yaw_deg, xy)] and the chosen one (best candidate within 2 m of the sphere)."""
    pad = 2.0
    lo = model_pts.min(0) - pad; hi = model_pts.max(0) + pad
    shape = (int((hi[1] - lo[1]) / res), int((hi[0] - lo[0]) / res))
    F = gaussian_filter(GR.raster(model_pts, lo, shape, res), 1.2); F = np.minimum(F / F.max() * 6, 1)
    cand = []
    for th in np.deg2rad(np.arange(0, 360, 1.0)):
        S = GR.raster(first_chunk @ rot(th).T + (lo + hi) / 2, lo, shape, res)
        C = fftconvolve(F, S[::-1, ::-1], mode="same") / len(first_chunk)
        pk = np.argwhere((C == maximum_filter(C, size=21)) & (C > min_score))
        for y, x in pk:
            cand.append((float(C[y, x]), float(np.rad2deg(th)), (lo + hi) / 2 + np.array([x - shape[1] // 2, y - shape[0] // 2]) * res))
    cand.sort(key=lambda r: -r[0])
    ranked = []
    for sc, th, xy in cand:
        if not any(np.hypot(*(xy - r[2])) < 1.0 and abs((th - r[1] + 180) % 360 - 180) < 10 for r in ranked):
            ranked.append((sc, th, xy))
        if len(ranked) >= 10:
            break
    near = [r for r in ranked if np.hypot(*(r[2] - sphere_xy)) < 2.0]
    return ranked, (near[0] if near else None)


def rigid_fit(pts, model_tree, model_pts, init, iters=60, keep=0.6):
    """Trimmed point-to-point ICP of an (unanchored) map onto the model; returns pose and diagnostics."""
    p = np.array(init, float)
    for _ in range(iters):
        Q = transform(p, pts); dd, j = model_tree.query(Q, distance_upper_bound=0.5); ok = np.isfinite(dd)
        ok &= dd <= np.quantile(dd[ok], keep)
        A_, B = Q[ok], model_pts[j[ok]]; ca, cb = A_.mean(0), B.mean(0)
        H = (A_ - ca).T @ (B - cb); dth = np.arctan2(H[0, 1] - H[1, 0], H[0, 0] + H[1, 1])
        t = cb - rot(dth) @ ca
        p = mat_pose(pose_mat([t[0], t[1], dth]) @ pose_mat(p))
    Q = transform(p, pts); dd, _ = model_tree.query(Q, distance_upper_bound=1.0)
    return p, {f"frac_within_{int(t*100)}cm": float(np.mean(dd < t)) for t in (0.05, 0.10, 0.20, 0.30)}


def anchor_trajectory(scans, kp, models, start_xy, T0, odo, heading, loops, rounds=(0.5, 0.3, 0.2), log_=log):
    """Sequential propagation from T0, piecewise-rigid init, then alternating graph solve / re-matching."""
    mm = A.ModelMatcher(models)
    Xm, anc = A.sequential_pass(scans, kp, T0, mm)
    log_.info("sequential pass: %d anchors", len(anc))
    X = A.init_from_anchors(Xm, anc)
    anc = A.parallel_pass(scans, X, mm, win=0.8, ang=np.deg2rad(4))
    info = []
    for r, w in enumerate(rounds):
        X, sol = A.solve_anchored(X, odo, heading, loops, anc, start=start_xy)
        anc = A.parallel_pass(scans, X, mm, win=w, ang=np.deg2rad(2))
        info.append(dict(round=r, solver_status=int(sol.status), cost=float(sol.cost), nfev=int(sol.nfev), anchors=len(anc),
                         inlier_median=float(np.median([a[2] for a in anc.values()])),
                         rms_median_m=float(np.median([a[3] for a in anc.values()]))))
        log_.info("anchor round %d: %s", r, info[-1])
    return X, anc, info
