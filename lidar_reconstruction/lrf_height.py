"""Sensor height above the floor from the downward-pointing laser range finder (LRF) log {t, d, s, sig}; same clock as the LiDAR.
d = distance to floor (m) at ~50 Hz. Assumptions: LRF axis vertical, LRF on the same vertical level as the LiDAR plane (offset 0), floor flat."""
import csv, json, os
import numpy as np
from scipy.ndimage import median_filter


def load_lrf(path):
    R = [json.loads(l) for l in open(path)]
    t = np.array([r["t"] for r in R]); d = np.array([r["d"] for r in R]); s = np.array([r["s"] for r in R]); sig = np.array([r["sig"] for r in R])
    ok = (s == 1) & np.isfinite(d) & (d > 0.3) & (d < 3.5)
    o = np.argsort(t[ok]); return t[ok][o], d[ok][o], sig[ok][o]


def scan_heights(lrf_path, scan_t, med_samples=9):
    """Median-filtered (9 samples ~0.18 s) LRF height interpolated to every scan timestamp."""
    t, d, _ = load_lrf(lrf_path); dm = median_filter(d, size=med_samples, mode="nearest")
    return np.interp(scan_t, t, dm)


def run(root):
    O = os.path.join(root, "output"); lt = np.load(f"{O}/lidar_cache.npz")["t0"]
    h = scan_heights(os.path.join(root, "data", "lrf.jsonl"), lt); np.save(f"{O}/scan_height_lrf.npy", h)
    with open(f"{O}/scan_height_lrf.csv", "w", newline="") as f:
        w = csv.writer(f); w.writerow(["scan_id", "t_s", "sensor_height_m_lrf"])
        for i in range(len(lt)): w.writerow([i, "%.4f" % lt[i], "%.3f" % h[i]])
    print("heights: min %.2f median %.2f max %.2f m; low state (<1.3 m) %.0f %% of scans" % (h.min(), np.median(h), h.max(), 100 * (h < 1.3).mean()))
    return h


if __name__ == "__main__":
    run(os.path.dirname(os.path.abspath(__file__)))
