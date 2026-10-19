"""Global 2D registration by FFT cross-correlation over rotation (and optional mirror) of a LiDAR point map to a model point set."""
import numpy as np
from scipy.signal import fftconvolve
from scipy.ndimage import gaussian_filter
from geometry import rot


def raster(pts, origin, shape, res):
    ij = np.floor((pts - origin) / res).astype(int)
    ok = (ij[:, 0] >= 0) & (ij[:, 0] < shape[1]) & (ij[:, 1] >= 0) & (ij[:, 1] < shape[0])
    img = np.zeros(shape, np.float32); np.add.at(img, (ij[ok, 1], ij[ok, 0]), 1.0); return img


def global_search(map_pts, model_pts, res=0.1, dang=0.5, blur=1.5, mirrors=(1, -1), pad=12.0):
    lo = model_pts.min(0) - pad; hi = model_pts.max(0) + pad
    shape = (int((hi[1] - lo[1]) / res), int((hi[0] - lo[0]) / res))
    F = gaussian_filter(raster(model_pts, lo, shape, res), blur); F = np.minimum(F / F.max() * 6, 1.0)
    out = []
    cen = map_pts.mean(0)
    for mir in mirrors:
        P0 = (map_pts - cen) * np.array([1, mir])
        for th in np.deg2rad(np.arange(0, 360, dang)):
            Q = P0 @ rot(th).T                       # centred; origin at map centroid
            # place at model-bbox centre; correlation lag gives translation
            mid = (lo + hi) / 2
            S = raster(Q + mid, lo, shape, res)
            C = fftconvolve(F, S[::-1, ::-1], mode="same")
            k = np.unravel_index(np.argmax(C), C.shape)
            lag = np.array([k[1] - shape[1] // 2, k[0] - shape[0] // 2]) * res
            # image of S shifted by +lag best matches F  => translation = lag (+ (shape odd/even) half-pixel ignored)
            out.append((float(C[k] / len(map_pts)), mir, float(np.rad2deg(th)), (mid + lag - 0) , cen))
    out.sort(key=lambda r: -r[0])
    return out
