"""SE(2) helpers and point-to-line scan matching. Poses are (x, y, yaw) in metres/radians; T maps sensor -> map."""
import numpy as np
from scipy.spatial import cKDTree


def rot(th):
    c, s = np.cos(th), np.sin(th)
    return np.array([[c, -s], [s, c]])


def pose_mat(p):
    T = np.eye(3); T[:2, :2] = rot(p[2]); T[:2, 2] = p[:2]
    return T


def mat_pose(T):
    return np.array([T[0, 2], T[1, 2], np.arctan2(T[1, 0], T[0, 0])])


def compose(a, b):
    return mat_pose(pose_mat(a) @ pose_mat(b))


def inverse(a):
    return mat_pose(np.linalg.inv(pose_mat(a)))


def relative(a, b):
    """Pose of b expressed in frame a."""
    return mat_pose(np.linalg.inv(pose_mat(a)) @ pose_mat(b))


def wrap(a):
    return (a + np.pi) % (2 * np.pi) - np.pi


def transform(p, pts):
    return pts @ rot(p[2]).T + p[:2]


def voxel_filter(pts, v):
    if len(pts) == 0:
        return pts
    k = np.floor(pts / v).astype(np.int64)
    _, idx = np.unique(k[:, 0] * 1000003 + k[:, 1], return_index=True)
    return pts[np.sort(idx)]


class Submap:
    """Static point set with local line normals for point-to-line matching."""

    def __init__(self, pts, knn=8):
        self.pts = pts
        self.tree = cKDTree(pts)
        k = min(knn, len(pts))
        _, nb = self.tree.query(pts, k=k)
        P = pts[nb]; P = P - P.mean(1, keepdims=True)
        C = np.einsum("nki,nkj->nij", P, P) / k
        w, v = np.linalg.eigh(C)
        self.normals = v[:, :, 0]                     # smallest-eigenvalue direction
        self.lin = 1 - w[:, 0] / np.maximum(w[:, 1], 1e-12)   # 1 = perfect line

    def match(self, scan, init, iters=15, gate=(0.6, 0.3, 0.15), huber=0.05):
        """Gauss-Newton point-to-line. Returns pose, rms, inlier_frac, hessian (3x3 information)."""
        p = np.array(init, float)
        H = np.eye(3) * 1e-6
        for it in range(iters):
            g = gate[min(it * len(gate) // iters, len(gate) - 1)]
            q = transform(p, scan)
            d, j = self.tree.query(q, distance_upper_bound=g)
            ok = np.isfinite(d) & (self.lin[np.minimum(j, len(self.pts) - 1)] > 0.5)
            if ok.sum() < 10:
                return p, np.inf, 0.0, H
            j = j[ok]; n = self.normals[j]
            e = np.einsum("ij,ij->i", n, q[ok] - self.pts[j])
            c, s = np.cos(p[2]), np.sin(p[2])
            dRp = np.c_[-s * scan[ok, 0] - c * scan[ok, 1], c * scan[ok, 0] - s * scan[ok, 1]]
            J = np.c_[n, np.einsum("ij,ij->i", n, dRp)]
            w = np.where(np.abs(e) < huber, 1.0, huber / np.maximum(np.abs(e), 1e-9))
            H = (J * w[:, None]).T @ J + np.eye(3) * 1e-6
            step = np.linalg.solve(H, -(J * w[:, None]).T @ e)
            p = p + step
            p[2] = wrap(p[2])
            if np.abs(step[:2]).max() < 1e-4 and abs(step[2]) < 1e-4:
                break
        q = transform(p, scan)
        d, j = self.tree.query(q, distance_upper_bound=0.15)
        ok = np.isfinite(d)
        rms = float(np.sqrt(np.mean(d[ok] ** 2))) if ok.any() else np.inf
        return p, rms, float(ok.mean()), H

    def score(self, scan, p, tol=0.08):
        d, _ = self.tree.query(transform(p, scan), distance_upper_bound=tol)
        return float(np.isfinite(d).mean())
