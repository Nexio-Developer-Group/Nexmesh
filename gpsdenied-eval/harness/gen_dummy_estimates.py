"""Control: ground truth + Gaussian noise (sigma m per axis) as an 'estimates.csv' so SITL runs have a reference."""
import csv, sys, os, numpy as np
sys.path.insert(0, os.path.dirname(__file__))
import run_replay as R
truth_p, out, sigma = sys.argv[1], sys.argv[2], float(sys.argv[3])
t = R.load_truth(truth_p); rng = np.random.default_rng(0)
os.makedirs(os.path.dirname(out), exist_ok=True)
with open(out, "w", newline="") as f:
    w = csv.writer(f); w.writerow(["t", "lat", "lon", "alt", "yaw", "conf", "accepted"])
    for i in range(len(t["t"])):
        n, e = rng.normal(0, sigma, 2)
        w.writerow([t["t"][i], t["lat"][i] + n / 111320.0, t["lon"][i] + e / (111320.0 * np.cos(np.radians(t["lat"][i]))), "", "", 50, True])
