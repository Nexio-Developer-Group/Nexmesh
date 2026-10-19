"""Register the CAD linework (PDF points) to the Blender section (metres): scale x rotation (0/90/180/270) x mirror x translation by FFT correlation, then similarity ICP."""
import json, numpy as np
from scipy.signal import fftconvolve
from scipy.ndimage import gaussian_filter
from scipy.spatial import cKDTree
import global_reg as GR
from geometry import rot


def dense(S, step):
    out = []
    for x0, y0, x1, y1 in S:
        L = np.hypot(x1 - x0, y1 - y0); n = max(2, int(L / step)); t = np.linspace(0, 1, n)[:, None]; out.append(np.array([x0, y0]) + t * np.array([x1 - x0, y1 - y0]))
    return np.vstack(out)


def register(S, model_pts, scales, res=0.1):
    lo = model_pts.min(0) - 6; hi = model_pts.max(0) + 6; shape = (int((hi[1] - lo[1]) / res), int((hi[0] - lo[0]) / res))
    F = gaussian_filter(GR.raster(model_pts, lo, shape, res), 1.2); F = np.minimum(F / F.max() * 6, 1)
    best = None
    for s in scales:
        P = dense(S, 0.05 / s) * s            # metres, PDF frame
        c0 = P.mean(0)
        for mir in (1, -1):
            for k in range(4):
                Q = ((P - c0) * np.array([mir, 1])) @ rot(k * np.pi / 2).T
                im = GR.raster(Q + (lo + hi) / 2, lo, shape, res); C = fftconvolve(F, im[::-1, ::-1], mode="same"); idx = np.unravel_index(np.argmax(C), C.shape)
                sc = C[idx] / len(Q); lag = np.array([idx[1] - shape[1] // 2, idx[0] - shape[0] // 2]) * res
                if best is None or sc > best[0]: best = (sc, s, mir, k, lag, c0, lo, hi)
    return best


def refine(S, best, model_pts, iters=40):
    sc, s, mir, k, lag, c0, lo, hi = best; tree = cKDTree(model_pts); P = dense(S, 0.05 / s)       # PDF points
    # current map: x_m = R_k (diag(mir,1)(s*(p) - c0)) + (lo+hi)/2 + lag   (c0 is in metres)
    def fwd(P, s, R, t): return s * P @ R.T + t
    D = np.diag([mir, 1.0]); Rk = rot(k * np.pi / 2); R = Rk @ D; t = (lo + hi) / 2 + lag - R @ c0
    sc_, Rm, tm = s, R, t
    for _ in range(iters):
        Q = sc_ * P @ Rm.T + tm; d, j = tree.query(Q, distance_upper_bound=0.3); ok = np.isfinite(d); ok &= d <= np.quantile(d[ok], 0.7)
        A, B = P[ok], model_pts[j[ok]]; ma, mb = A.mean(0), B.mean(0); H = (B - mb).T @ (A - ma) / len(A)
        U, Dg, Vt = np.linalg.svd(H); Sg = np.eye(2); Sg[1, 1] = np.sign(np.linalg.det(U @ Vt)); Rn = U @ Sg @ Vt
        if np.linalg.det(Rm) < 0: pass
        var = ((A - ma) ** 2).sum() / len(A); sc_ = float((Dg * np.diag(Sg)).sum() / var); Rm = Rn; tm = mb - sc_ * Rm @ ma
    Q = sc_ * P @ Rm.T + tm; d, _ = tree.query(Q, distance_upper_bound=1.0)
    return dict(scale_m_per_pt=sc_, R=Rm.tolist(), t=tm.tolist(), frac_within_5cm=float(np.mean(d < .05)), frac_within_10cm=float(np.mean(d < .10)), median_m=float(np.median(d[d < 1])), det=float(np.linalg.det(Rm)))


if __name__ == "__main__":
    import cad_extract
    S, words, size = cad_extract.extract("data/cad_plan.pdf")
    seg = json.load(open("output/model_slice.json"))
    mp = []
    for q in seg:
        if q["kind"] in ("Wall", "Column", "Window", "Door"):
            a, b = np.array(q["p"][:2]), np.array(q["p"][2:]); n = max(2, int(np.linalg.norm(b - a) / 0.03)); mp.append(a + (b - a) * np.linspace(0, 1, n)[:, None])
    mp = np.vstack(mp)
    best = register(S, mp, np.arange(0.0398, 0.0418, 0.0002)); print("coarse: score %.3f scale %.4f mirror %d rot %d" % best[:4])
    r = refine(S, best, mp); print(r)
    json.dump(r, open("output/cad_registration.json", "w"), indent=1)
