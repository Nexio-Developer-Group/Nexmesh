"""Driver for S6 tests. Uses harness/run_replay.run (frames at 5 Hz) + harness/metrics.summarize; writes extras.csv, metrics.json, log.txt.
Usage: python driver.py --run-id S6-T1-20261023-s0 [--stride 1] [--cfg '{"tracking":true}'] [--mode offline|realtime] [--att truth60|hints]
Writes results/s6/nidar/<run-id>/.  Frames subsampled by --stride (5 -> 1 Hz) via a temporary symlink dir."""
import argparse, json, os, sys, csv, time, shutil, platform, subprocess
HERE = os.path.dirname(os.path.abspath(__file__)); ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
sys.path.insert(0, HERE); sys.path.insert(0, os.path.join(ROOT, "harness"))
import numpy as np
import run_replay, metrics
import adapter as AD


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--run-id", required=True); ap.add_argument("--stride", type=int, default=1)
    ap.add_argument("--cfg", default="{}"); ap.add_argument("--mode", default="offline"); ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--att", default="truth60"); ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--out-root", default=os.path.join(ROOT, "results", "s6", "nidar"))
    a = ap.parse_args()
    cfg = json.loads(a.cfg); cfg["att_source"] = a.att
    out = os.path.join(a.out_root, a.run_id); os.makedirs(out, exist_ok=True)
    frames = os.path.join(ROOT, "data", "nidar", "frames")
    fl = sorted(os.listdir(frames), key=lambda f: float(os.path.splitext(f)[0]))[::a.stride]
    if a.limit: fl = fl[:a.limit]
    tmp = os.path.join("/tmp", "s6_frames_" + a.run_id); shutil.rmtree(tmp, ignore_errors=True); os.makedirs(tmp)
    for f in fl: os.symlink(os.path.join(frames, f), os.path.join(tmp, "%09.3f.png" % float(os.path.splitext(f)[0])))  # zero-padded: harness sorts names as strings
    truth = run_replay.load_truth(os.path.join(ROOT, "data", "nidar", "truth.csv"))
    ad = AD.Adapter()
    est, meta = run_replay.run(ad, tmp, truth, os.path.join(ROOT, "data", "nidar", "map_esri_z19.png"), out, a.mode, seed=a.seed, config=cfg)
    n = len(fl)
    e = {k: np.array([r.get(k) for r in est]) for k in ("t", "lat", "lon", "accepted")}
    e["lat"] = e["lat"].astype(float); e["lon"] = e["lon"].astype(float); e["accepted"] = e["accepted"].astype(bool)
    m = metrics.summarize(e, truth, n)
    m["latency_ms"] = metrics.latency_stats(meta["latency_ms"]); m["config"] = {**ad.pipe.cfg, "att_source": a.att}
    m["init"] = ad.init_info; m["stride"] = a.stride; m["note"] = "synthetic NIDAR-like; map == render source; CPU 2 threads, shared load (latency untrustworthy)"
    m["cpu"] = platform.processor() or "Intel Xeon 2.10GHz"; m["seed"] = a.seed
    m["dropped"] = meta["dropped"]
    json.dump(m, open(os.path.join(out, "metrics.json"), "w"), indent=1, default=str)
    ex = ad.extras
    if ex:
        keys = sorted({k for r in ex for k in r}, key=lambda k: (k != "t", k))
        with open(os.path.join(out, "extras.csv"), "w", newline="") as fh:
            w = csv.DictWriter(fh, keys); w.writeheader(); w.writerows(ex)
    # per-run log line
    errs = metrics.position_errors({k: v[e["accepted"]] for k, v in e.items()}, truth) if e["accepted"].any() else []
    with open(os.path.join(out, "log.txt"), "w") as fh:
        fh.write(f"run {a.run_id} frames={n} accepted={m['accepted']} fix_rate={m['fix_rate']:.1f}% median={m['median']} p95={m['p95']} rmse={m['rmse']} prec100={m['precision_100']}\n")
        fh.write(json.dumps({"init": ad.init_info, "latency": m["latency_ms"]}) + "\n")
    print(open(os.path.join(out, "log.txt")).read())


if __name__ == "__main__":
    main()
