"""Model-anchored back-end. Starting from the known start (Blender sphere), propagate along the trajectory, match each
keyframe chunk to the Blender section at LiDAR height, and use the matches as loose robust priors in the pose graph.
LiDAR scan geometry itself is never altered; only keyframe poses are constrained."""
import json, logging
import numpy as np
from geometry import *
import pose_graph as G
import graph_solver as M

log = logging.getLogger("anchor")


def model_points(slice_json, kinds=("Wall", "Column", "Window", "Door"), step=0.03):
    out = []
    for s in json.load(open(slice_json)):
        if s["kind"] in kinds:
            a = np.array(s["p"][:2]); b = np.array(s["p"][2:])
            n = max(2, int(np.linalg.norm(b - a) / step)); out.append(a + (b - a) * np.linspace(0, 1, n)[:, None])
    return np.vstack(out)


class ModelMatcher:
    def __init__(self, pts):
        self.pts = pts
        self.sub = Submap(pts, knn=6)

    def match(self, chunk, pred, win, ang, fft=True):
        p = pred
        sc = 0.0
        if fft:
            crop = self.pts[np.hypot(*(self.pts - pred[:2]).T) < 9 + win + 3]
            if len(crop) < 20:
                return pred, 0.0, np.inf, 0.0, np.eye(3) * 1e-6
            p, sc, _ = G.match_submaps(crop, chunk, pred, win=win, ang=ang, res=0.1, dang=np.deg2rad(1.0))
        p, rms, inl, H = self.sub.match(chunk, p, gate=(0.3, 0.2, 0.12))
        return p, sc, rms, inl, H


def sequential_pass(scans, X, T0, matcher, step=3, half=6, win0=1.0, ang0=np.deg2rad(5), min_inl=0.55, max_rms=0.06):
    """Walk through chunks in time order; each prediction = last accepted anchor composed with odometry."""
    n = len(X)
    Xm = np.array([compose(T0, x) for x in X])
    anchors = {}
    last = None                 # (c, pose in model frame)
    misses = 0
    for c in range(half, n - half, step):
        chunk = voxel_filter(M.chunk_scan(scans, Xm, c, half), 0.04)      # local geometry, odometry-consistent
        pred = Xm[c] if last is None else compose(last[1], relative(Xm[last[0]], Xm[c]))
        w = win0 + 0.35 * misses; a = ang0 + np.deg2rad(1.0) * misses
        p, sc, rms, inl, H = matcher.match(chunk, pred, min(w, 3.5), min(a, np.deg2rad(12)))
        if inl >= min_inl and rms <= max_rms and np.hypot(*(p[:2] - pred[:2])) <= w + 0.3:
            anchors[c] = (p, H, inl, rms); last = (c, p); misses = 0
        else:
            misses += 1
            if last is not None:        # keep dead-reckoned estimate relative to last anchor
                pass
    return Xm, anchors


def parallel_pass(scans, Xm, matcher, step=2, half=6, win=0.5, ang=np.deg2rad(3), min_inl=0.55, max_rms=0.06):
    anchors = {}
    for c in range(half, len(Xm) - half, step):
        chunk = voxel_filter(M.chunk_scan(scans, Xm, c, half), 0.04)
        p, sc, rms, inl, H = matcher.match(chunk, Xm[c], win, ang, fft=False)
        if inl >= min_inl and rms <= max_rms and np.hypot(*(p[:2] - Xm[c, :2])) <= win:
            anchors[c] = (p, H, inl, rms)
    return anchors


def solve_anchored(Xm, odo, heading, loops, anchors, sig=0.10, s_odo=(0.04, np.deg2rad(0.6)), s_loop=(0.06, np.deg2rad(0.7)),
                   start=None, s_start=0.25):
    pri = []
    for c, (p, H, inl, rms) in anchors.items():
        Lc = np.linalg.cholesky(H / sig ** 2 / 50.0 + np.eye(3) * 1e-3)
        pri.append((c, p, Lc))
    if start is not None:           # soft start constraint from the sphere (x, y only); sigma ~ sphere radius
        pri.append((0, np.array([start[0], start[1], Xm[0, 2]]), np.diag([1 / s_start, 1 / s_start, 0.0])))
    X, sol = M.solve(Xm, odo, heading, loops, pri, s_odo, s_loop, max_nfev=300, fix_first=False)
    return X, sol


def init_from_anchors(Xm, anchors):
    """Piecewise-rigid initialisation: each keyframe is placed relative to its nearest accepted anchor."""
    cs = np.array(sorted(anchors))
    out = Xm.copy()
    for k in range(len(Xm)):
        c = cs[np.argmin(np.abs(cs - k))]
        out[k] = compose(anchors[c][0], relative(Xm[c], Xm[k]))
    return out
