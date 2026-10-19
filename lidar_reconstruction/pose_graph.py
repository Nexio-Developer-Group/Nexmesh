"""Loop-closure detection (FFT correlative scan-map matching + ICP verification) and robust SE(2) pose-graph optimisation."""
import logging
import numpy as np
from scipy.signal import fftconvolve
from scipy.ndimage import gaussian_filter
from scipy.spatial import cKDTree
from geometry import *

log = logging.getLogger("pg")


def local_maps(kf_scans, kf_pose, w=8, rmax=9.0, vox=0.05):
    """Map around each keyframe, expressed in that keyframe's sensor frame (uses odometry poses: locally consistent)."""
    out = []
    for k in range(len(kf_pose)):
        lo, hi = max(0, k - w), min(len(kf_pose), k + w + 1)
        pts = np.vstack([transform(relative(kf_pose[k], kf_pose[m]), kf_scans[m]) for m in range(lo, hi)])
        pts = pts[np.hypot(pts[:, 0], pts[:, 1]) < rmax]
        out.append(voxel_filter(pts, vox))
    return out


def _raster(pts, origin, n, res):
    ij = np.floor((pts - origin) / res).astype(int)
    ok = (ij >= 0).all(1) & (ij < n).all(1)
    img = np.zeros((n, n), np.float32)
    np.add.at(img, (ij[ok, 1], ij[ok, 0]), 1.0)
    return img


def match_submaps(tgt, src, pred, win=3.5, ang=np.deg2rad(14), res=0.1, dang=np.deg2rad(1.0)):
    """Find T (src frame -> tgt frame) near `pred`. Returns (pose, score, runner_up_score)."""
    R = 9.0 + win + 0.5
    n = int(2 * R / res); origin = pred[:2] - R
    F = gaussian_filter(_raster(tgt, origin, n, res), 1.5); F /= max(F.max(), 1e-9)
    F = np.minimum(F * 4, 1.0)
    cands = []
    maxlag = int(win / res)
    c0 = n // 2
    for dth in np.arange(-ang, ang + 1e-9, dang):
        p = np.array([pred[0], pred[1], pred[2] + dth])
        S = _raster(transform(p, src), origin, n, res)
        C = fftconvolve(F, S[::-1, ::-1], mode="same")      # C[c0+dy,c0+dx] = sum F(x+d) S(x)
        sub = C[c0 - maxlag:c0 + maxlag + 1, c0 - maxlag:c0 + maxlag + 1]
        cands.append(sub / len(src))
    cs = np.array(cands)
    k = np.unravel_index(np.argmax(cs), cs.shape)
    best = cs[k]
    # runner-up: best score away from the peak (>0.6 m, >3 deg)
    mask = np.ones_like(cs, bool)
    a0, y0, x0 = k
    mask[max(0, a0 - 3):a0 + 4, max(0, y0 - 6):y0 + 7, max(0, x0 - 6):x0 + 7] = False
    second = cs[mask].max()
    dth = -ang + k[0] * dang
    return np.array([pred[0] + (k[2] - maxlag) * res, pred[1] + (k[1] - maxlag) * res, wrap(pred[2] + dth)]), float(best), float(second)


_G = {}


def _try_pair(args):
    j, k, pred, kw = args
    maps = _G["maps"]
    p, sc, sec = match_submaps(maps[j], maps[k], pred, win=kw["win"], ang=kw["ang"])
    if sc < kw["min_score"]:
        return None
    sub = Submap(maps[j])
    p, rms, inl, H = sub.match(maps[k], p, gate=(0.3, 0.2, 0.1))
    shift = float(np.hypot(*(p[:2] - pred[:2])))
    if inl >= kw["min_inl"] and rms <= kw["max_rms"]:
        return (j, k, p, inl, rms, sc, sec, shift)
    return None


def find_loops(maps, kf_pose, min_sep=100, radius=6.0, step=3, per_query=4, win=3.0, ang=np.deg2rad(5),
               min_score=0.5, min_inl=0.75, max_rms=0.04, workers=4):
    import multiprocessing as mp
    xy = kf_pose[:, :2]
    tree = cKDTree(xy)
    jobs = []
    for k in range(min_sep, len(kf_pose), step):
        idx = [j for j in tree.query_ball_point(xy[k], radius) if k - j > min_sep]
        idx.sort(key=lambda j: np.hypot(*(xy[j] - xy[k])))
        chosen = []
        for j in idx:
            if all(abs(j - c) > 12 for c in chosen):
                chosen.append(j)
            if len(chosen) >= per_query:
                break
        jobs += [(j, k, relative(kf_pose[j], kf_pose[k]), dict(win=win, ang=ang, min_score=min_score, min_inl=min_inl, max_rms=max_rms))
                 for j in chosen]
    log.info("loop candidates: %d pairs", len(jobs))
    _G["maps"] = maps
    with mp.get_context("fork").Pool(workers) as pool:
        res = pool.map(_try_pair, jobs, chunksize=8)
    return [r for r in res if r is not None], len(jobs)


def manhattan_headings(maps, min_pts=80, min_lin_pts=40, strength_min=0.6):
    """Per-keyframe dominant wall direction alpha (mod 90 deg) in the sensor frame, from line normals (4-fold circular mean).
    Returns alpha[k], strength[k]; global phi (wall direction in the SLAM frame) from the strong keyframes."""
    al = np.full(len(maps), np.nan); stg = np.zeros(len(maps))
    for k, m in enumerate(maps):
        if len(m) < min_pts:
            continue
        s = Submap(m); ok = s.lin > 0.9
        if ok.sum() < min_lin_pts:
            continue
        v = np.exp(4j * np.arctan2(s.normals[ok, 1], s.normals[ok, 0])).sum()
        al[k] = np.angle(v) / 4; stg[k] = abs(v) / ok.sum()
    return al, stg
