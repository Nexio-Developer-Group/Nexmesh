"""Driver: replay with extras saved. Usage: run_s3.py --tag S3-T1 [--step 1] [--derotate] [--no-scale-norm] [--topk N] [--max-cand N]"""
import argparse, csv, json, os, sys, time, datetime
import numpy as np
HERE = os.path.dirname(os.path.abspath(__file__)); ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
sys.path.insert(0, os.path.join(ROOT, "harness")); sys.path.insert(0, HERE)
import run_replay as R, metrics as M
from adapter import Adapter

ap = argparse.ArgumentParser()
ap.add_argument("--tag", required=True); ap.add_argument("--step", type=int, default=1)
ap.add_argument("--derotate", action="store_true"); ap.add_argument("--no-scale-norm", action="store_true")
ap.add_argument("--hints", action="store_true"); ap.add_argument("--topk", type=int, default=5)
ap.add_argument("--max-cand", type=int, default=3); ap.add_argument("--mode", default="offline")
ap.add_argument("--seed", type=int, default=0); ap.add_argument("--dataset", default="nidar")
ap.add_argument("--limit", type=int, default=0)
a = ap.parse_args()
data = os.path.join(ROOT, "data", a.dataset)
truth = R.load_truth(os.path.join(data, "truth.csv"))
frames = os.path.join(data, "frames")
if a.step > 1 or a.limit:  # subsample into a temp list dir of symlinks
    import tempfile
    tmp = tempfile.mkdtemp(); fs = sorted(os.listdir(frames), key=lambda f: float(f[:-4]))[:: a.step]
    if a.limit: fs = fs[: a.limit]
    for f in fs: os.symlink(os.path.join(frames, f), os.path.join(tmp, f))
    frames = tmp
rid = f"{a.tag}-{datetime.date.today():%Y%m%d}-s{a.seed}"
out = os.path.join(ROOT, "results", "s3", a.dataset, rid)
cfg = dict(derotate=a.derotate, scale_norm=not a.no_scale_norm, topk=a.topk, max_candidates=a.max_cand)
ad = Adapter()
est, meta = R.run(ad, frames, truth, os.path.join(data, "map_esri_z19.png"), out, a.mode, seed=a.seed,
                  hints_on=(a.hints or a.derotate), config=cfg)
keys = ["t", "lat", "lon", "accepted", "flag", "n_inliers", "top1_sim", "conf"]
with open(os.path.join(out, "estimates_full.csv"), "w", newline="") as f:
    w = csv.DictWriter(f, keys, extrasaction="ignore"); w.writeheader(); w.writerows(est)
E = {k: np.array([r[k] for r in est]) for k in ("t", "lat", "lon", "accepted")}
n = len(est)
res = {"all_verified": M.summarize(E, truth, n)}
fb = {k: np.array([r[k] for r in est if r["flag"] == "fallback_tile_centre"]) for k in ("t", "lat", "lon")}
if len(fb["t"]):
    fb["accepted"] = np.ones(len(fb["t"]), bool); res["fallback_only"] = M.summarize(fb, truth, n)
res["config"] = cfg; res["step"] = a.step; res["n_frames"] = n; res["startup_s"] = meta["startup_s"]
res["index_build_s"] = ad.index_build_s; res["index_mb"] = ad.index_mb; res["tiles"] = len(ad.tiles); res["scale_ratio"] = ad.ratio
res["latency_ms"] = M.latency_stats(meta["latency_ms"]); res["peak_rss_mb"] = meta["peak_rss_mb"]
json.dump(res, open(os.path.join(out, "metrics.json"), "w"), indent=1)
print(json.dumps({k: res[k] for k in ("all_verified", "latency_ms", "index_build_s", "tiles")}, indent=1))
