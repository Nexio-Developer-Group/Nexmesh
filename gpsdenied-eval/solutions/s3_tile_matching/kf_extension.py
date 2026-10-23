"""S3-T8 OUR EXTENSION (not the author's method): constant-velocity Kalman filter with a Mahalanobis gate over S3's verified fixes.
Post-processes results/s3/nidar/<run>/estimates.csv. Reports error and jump reduction against the raw fixes."""
import csv, json, math, os, sys
import numpy as np
ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
sys.path.insert(0, os.path.join(ROOT, "harness"))
import metrics as M, run_replay as R
run = sys.argv[1] if len(sys.argv) > 1 else "S3-T1-20261023-s0"
d = os.path.join(ROOT, "results/s3/nidar", run)
rows = list(csv.DictReader(open(os.path.join(d, "estimates.csv"))))
truth = R.load_truth(os.path.join(ROOT, "data/nidar/truth.csv"))
lat0, lon0 = truth["lat"][0], truth["lon"][0]
k = 111320.0; kl = k * math.cos(math.radians(lat0))
def xy(la, lo): return np.array([(lo - lon0) * kl, (la - lat0) * k])
def ll(p): return lat0 + p[1] / k, lon0 + p[0] / kl
SIG, Q, GATE = 10.0, 1.0, 9.21  # m fix noise, accel noise (m/s2), chi2(2) 99%
x = None; P = None; out = []; prev_t = None; rejected = 0
for r in rows:
    t = float(r["t"]); acc = r["accepted"] in ("True", "true", "1"); z = xy(float(r["lat"]), float(r["lon"]))
    if x is None:
        if not acc: out.append((t, None, None, False)); continue
        x = np.r_[z, 0, 0]; P = np.diag([SIG ** 2, SIG ** 2, 100, 100]); prev_t = t; out.append((t, *ll(x[:2]), True)); continue
    dt = t - prev_t; prev_t = t
    F = np.eye(4); F[0, 2] = F[1, 3] = dt
    G = np.array([[dt ** 2 / 2, 0], [0, dt ** 2 / 2], [dt, 0], [0, dt]])
    x = F @ x; P = F @ P @ F.T + G @ G.T * Q ** 2
    if acc:
        H = np.array([[1, 0, 0, 0], [0, 1, 0, 0]]); S = H @ P @ H.T + np.eye(2) * SIG ** 2; y = z - H @ x
        if y @ np.linalg.solve(S, y) <= GATE:
            K = P @ H.T @ np.linalg.inv(S); x = x + K @ y; P = (np.eye(4) - K @ H) @ P
        else: rejected += 1
    out.append((t, *ll(x[:2]), True))
est = {"t": np.array([o[0] for o in out if o[1] is not None]), "lat": np.array([o[1] for o in out if o[1] is not None]),
       "lon": np.array([o[2] for o in out if o[1] is not None]), "accepted": np.ones(sum(o[1] is not None for o in out), bool)}
res = {"kf_output_all_frames": M.summarize(est, truth, len(rows)), "gated_out_fixes": rejected}
raw = {"t": np.array([float(r["t"]) for r in rows]), "lat": np.array([float(r["lat"]) for r in rows]), "lon": np.array([float(r["lon"]) for r in rows]),
       "accepted": np.array([r["accepted"] in ("True", "true", "1") for r in rows])}
res["raw_verified_fixes"] = M.summarize(raw, truth, len(rows))
def jumps(e):
    p = np.array([xy(a, b) for a, b in zip(e["lat"], e["lon"])]); return float(np.mean(np.linalg.norm(np.diff(p, axis=0), axis=1))) if len(p) > 1 else None
rv = {k_: v[raw["accepted"]] for k_, v in raw.items()}
res["mean_step_m"] = {"raw_verified": jumps(rv), "kf": jumps(est)}
res["note"] = "OUR EXTENSION, not the author's method; KF outputs a position for every frame after the first fix (predicted when no fix), so recall is not comparable to the raw verified-fix recall"
os.makedirs(os.path.join(ROOT, "results/s3/nidar", run + "-KF"), exist_ok=True)
json.dump(res, open(os.path.join(ROOT, "results/s3/nidar", run + "-KF", "metrics.json"), "w"), indent=1)
with open(os.path.join(ROOT, "results/s3/nidar", run + "-KF", "estimates.csv"), "w", newline="") as f:
    w = csv.writer(f); w.writerow(["t", "lat", "lon", "accepted"]); [w.writerow([o[0], o[1], o[2], o[3]]) for o in out if o[1] is not None]
print(json.dumps({k_: (v if not isinstance(v, dict) else {a: (round(b, 2) if isinstance(b, float) else b) for a, b in v.items()}) for k_, v in res.items() if k_ != "note"}, indent=0))
