"""SE(2) pose-graph solver: odometry edges, loop edges, Manhattan heading factors and robust unary pose priors."""
import numpy as np
from scipy.optimize import least_squares
from scipy.sparse import lil_matrix
from geometry import *


def chunk_scan(scans, X, c, half=6, rmax=10.0):
    lo, hi = max(0, c - half), min(len(X), c + half + 1)
    pts = np.vstack([transform(relative(X[c], X[m]), scans[m]) for m in range(lo, hi)])
    return pts[np.hypot(pts[:, 0], pts[:, 1]) < rmax]


def solve(X0, odo, heading, loops, priors, s_odo, s_loop, s_head=np.deg2rad(1.5), max_nfev=40, fix_first=True):
    n = len(X0)
    ne_o, ne_l, ne_h, ne_p = len(odo), len(loops), len(heading), len(priors)
    m = 3 * (ne_o + ne_l) + ne_h + 3 * ne_p

    def res(x):
        X = x.reshape(-1, 3); r = np.empty(m); o = 0
        for edges, s in ((odo, s_odo), (loops, s_loop)):
            for (i, j, z) in edges:
                dt = rot(X[i, 2]).T @ (X[j, :2] - X[i, :2])
                r[o:o + 2] = (dt - z[:2]) / s[0]; r[o + 2] = wrap(X[j, 2] - X[i, 2] - z[2]) / s[1]; o += 3
        for (i, al, phi) in heading:
            r[o] = (((X[i, 2] + al - phi + np.pi / 4) % (np.pi / 2)) - np.pi / 4) / s_head; o += 1
        for (c, p, Lc) in priors:
            e = np.r_[X[c, :2] - p[:2], wrap(X[c, 2] - p[2])]
            r[o:o + 3] = Lc.T @ e; o += 3
        return r

    sp = lil_matrix((m, 3 * n), dtype=int); o = 0
    for edges in (odo, loops):
        for (i, j, _) in edges:
            sp[o:o + 3, 3 * i:3 * i + 3] = 1; sp[o:o + 3, 3 * j:3 * j + 3] = 1; o += 3
    for (i, *_) in heading:
        sp[o, 3 * i + 2] = 1; o += 1
    for (c, *_) in priors:
        sp[o:o + 3, 3 * c:3 * c + 3] = 1; o += 3
    x0 = X0.ravel().copy(); lo = np.full_like(x0, -np.inf); hi = np.full_like(x0, np.inf)
    if fix_first:                       # gauge freedom when nothing else pins the map
        lo[:3] = x0[:3] - 1e-9; hi[:3] = x0[:3] + 1e-9
    sol = least_squares(res, x0, jac_sparsity=sp, loss="cauchy", f_scale=2.0, bounds=(lo, hi), max_nfev=max_nfev)
    X = sol.x.reshape(-1, 3); X[:, 2] = wrap(X[:, 2])
    return X, sol
