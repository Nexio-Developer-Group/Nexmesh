"""S1 test driver. Uses harness load_truth + metrics.summarize (same metric code).

Examples (run from repo root with solutions/s1_gnss_denied_nav/env/bin/python, under `nice`):
  T1 : driver.py --id S1-T1 --stride 1
  T2 : driver.py --id S1-T2 --stride 5 --config '{"tracking":true}'
  T3 : driver.py --id S1-T3 --mode realtime --config '{"tracking":true}'      (lead runs, alone)
  T9 : driver.py --id S1-T9 --stride 5 --limit 61 --config '{"tracking":true}' --inject --inject-set early   (T9late = default late windows)
"""
import argparse, csv, json, os, platform, resource, subprocess, sys, time
from pathlib import Path
import cv2, numpy as np

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
sys.path.insert(0, str(ROOT / "harness")); sys.path.insert(0, str(HERE))
import run_replay, metrics  # noqa: E402
import adapter as adapter_mod  # noqa: E402

# failure-injection windows (t seconds from cruise start); chosen outside the video's own blur/haze windows
INJECT = {"blur": (35.0, 45.0), "lowlight": (72.0, 82.0), "wrong_heading": (110.0, 122.0), "drop50": (136.0, 152.8)}
# "early" set: windows inside the period where S1 holds lock (0-48 s) so injected faults act on a live track
INJECT_EARLY = {"wrong_heading": (3.0, 9.0), "drop50": (12.0, 20.0), "lowlight": (33.0, 38.0), "blur": (40.0, 45.0)}


def corrupt(img, t, rng):
    """Returns (img or None if dropped, list of active corruptions)."""
    act = []
    if INJECT["blur"][0] <= t < INJECT["blur"][1]:
        img = cv2.GaussianBlur(img, (0, 0), 8.0); act.append("blur")
    if INJECT["lowlight"][0] <= t < INJECT["lowlight"][1]:
        f = (img.astype(np.float32) / 255.0) ** 1.8 * 0.12
        f = f + rng.normal(0, 0.012, f.shape)
        img = np.clip(f * 255.0, 0, 255).astype(np.uint8); act.append("lowlight")
    if INJECT["drop50"][0] <= t < INJECT["drop50"][1] and rng.random() < 0.5:
        return None, ["dropped"]
    return img, act


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--id", required=True); ap.add_argument("--stride", type=int, default=1)
    ap.add_argument("--config", default="{}"); ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--mode", default="offline", choices=["offline", "realtime"])
    ap.add_argument("--inject", action="store_true"); ap.add_argument("--inject-set", default="late", choices=["late", "early"]); ap.add_argument("--limit", type=int, default=0)
    ap.add_argument("--no-hints", action="store_true")
    ap.add_argument("--frames", default=str(ROOT / "data/nidar/frames"))
    ap.add_argument("--truth", default=str(ROOT / "data/nidar/truth.csv"))
    ap.add_argument("--map", default=str(ROOT / "data/nidar/map_esri_z19.png"))
    ap.add_argument("--date", default=time.strftime("%Y%m%d"))
    ap.add_argument("--out", default=None)
    a = ap.parse_args()
    if a.inject_set == "early": INJECT.clear(); INJECT.update(INJECT_EARLY)
    run_id = f"{a.id}-{a.date}-s{a.seed}"
    out = Path(a.out or ROOT / "results/s1/nidar" / run_id); out.mkdir(parents=True, exist_ok=True)
    log = open(out / "log.txt", "w")
    def P(*s):
        m = " ".join(str(x) for x in s); print(m, flush=True); log.write(m + "\n"); log.flush()
    rng = np.random.default_rng(a.seed); np.random.seed(a.seed)
    import torch; torch.manual_seed(a.seed)
    cfg = json.loads(a.config)
    truth = run_replay.load_truth(a.truth)
    ad = adapter_mod.Adapter()
    t0 = time.time(); ad.init(a.map, cfg); startup = time.time() - t0
    P(f"run {run_id} cmd={' '.join(sys.argv)}\nconfig={ad.cfg}\nstartup_s={startup:.1f} build_info={ad.build_info}")
    files = sorted((f for f in os.listdir(a.frames) if f.endswith(".png")), key=lambda f: float(f[:-4]))   # numeric (lexicographic sort of harness/run_replay.py misorders 100.0 before 20.2)
    files = files[::a.stride]
    if a.limit: files = files[:a.limit]
    times = np.array([float(f[:-4]) for f in files])
    rows, lat_ms, dropped, clock0, busy_until = [], [], 0, time.time(), 0.0
    for k, (f, t) in enumerate(zip(files, times)):
        if a.mode == "realtime":
            now = time.time() - clock0
            if now < t - times[0]: time.sleep(t - times[0] - now)
            if (time.time() - clock0) > (t - times[0]) + 0.2 and busy_until > (t - times[0]):
                dropped += 1; continue
        img = cv2.imread(os.path.join(a.frames, f)); act = []
        if a.inject:
            img, act = corrupt(img, t, rng)
        i = int(np.argmin(np.abs(truth["t"] - t)))
        hints = None if a.no_hints else {"baro_alt": float(truth["alt"][i]), "att": (0.0, 0.0, float(truth["yaw"][i]))}
        if a.inject and hints and INJECT["wrong_heading"][0] <= t < INJECT["wrong_heading"][1]:
            hints["att"] = (0.0, 0.0, (hints["att"][2] + 90.0) % 360.0); act.append("wrong_heading")
        if img is None:
            r = dict(lat=float("nan"), lon=float("nan"), alt=None, yaw=None, conf=0.0, accepted=False, mode="dropped", gate="dropped")
            d = 0.0
        else:
            s = time.perf_counter(); r = ad.step(img, float(t), hints); d = time.perf_counter() - s
            lat_ms.append(d * 1000)
        busy_until = (t - times[0]) + d
        r["t"] = float(t); r["corrupt"] = "+".join(act)
        tl, tn = metrics.interp_truth(truth, np.array([t]))
        r["err_m"] = float(metrics.haversine(tl[0], tn[0], r["raw_lat"], r["raw_lon"])) if "raw_lat" in r and np.isfinite(r.get("raw_lat", np.nan)) else float("nan")
        r["err_acc_m"] = r["err_m"] if r["accepted"] else float("nan")
        rows.append(r)
        if k % 10 == 0 or a.limit:
            P(f"[{k+1}/{len(files)}] t={t:.1f} acc={r['accepted']} mode={r.get('mode')} gate={r.get('gate')} "
              f"inl={r.get('n_inliers')} err_raw={r['err_m']:.1f} ms={d*1000:.0f} {r['corrupt']}")
    keys = ["t", "lat", "lon", "alt", "yaw", "conf", "accepted"]
    with open(out / "estimates.csv", "w", newline="") as fh:
        w = csv.DictWriter(fh, keys, extrasaction="ignore"); w.writeheader(); w.writerows(rows)
    xk = ["t", "accepted", "mode", "gate", "n_inliers", "n_matches", "rank", "sim_top1", "second_inliers", "height_m",
          "off_nadir_deg", "reproj_px", "raw_lat", "raw_lon", "err_m", "err_acc_m", "ms", "corrupt"]
    with open(out / "extras.csv", "w", newline="") as fh:
        w = csv.DictWriter(fh, xk, extrasaction="ignore"); w.writeheader(); w.writerows(rows)
    est = {"t": np.array([r["t"] for r in rows]), "lat": np.array([r["lat"] for r in rows], float),
           "lon": np.array([r["lon"] for r in rows], float), "accepted": np.array([r["accepted"] for r in rows])}
    m = metrics.summarize(est, truth, len(rows))
    # extra: accepted-fix precision at 10/50/100 m, false fixes (accepted and >50 m)
    err_acc = np.array([r["err_acc_m"] for r in rows]); ok = np.isfinite(err_acc)
    m["accepted_within_10"] = float(100 * np.mean(err_acc[ok] <= 10)) if ok.any() else None
    m["accepted_within_50"] = float(100 * np.mean(err_acc[ok] <= 50)) if ok.any() else None
    m["false_fixes_gt50"] = int(np.sum(err_acc[ok] > 50)); m["false_fixes_gt100"] = int(np.sum(err_acc[ok] > 100))
    m["frames_processed"] = int(len(lat_ms)); m["stride"] = a.stride
    if lat_ms: m["latency_ms_p50"] = float(np.percentile(lat_ms, 50)); m["latency_ms_p95"] = float(np.percentile(lat_ms, 95))
    m["run_id"] = run_id; m["data"] = "synthetic NIDAR-like"; m["config"] = ad.cfg
    json.dump(m, open(out / "metrics.json", "w"), indent=1)
    cpu = [l for l in open("/proc/cpuinfo") if l.startswith("model name")][0].split(":")[1].strip()
    json.dump({"mode": a.mode, "seed": a.seed, "startup_s": startup, "frames_total": len(files), "frames_processed": len(lat_ms),
               "dropped": dropped, "latency_ms": lat_ms, "peak_rss_mb": resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024,
               "cpu": cpu, "stride": a.stride, "config": ad.cfg, "build_info": ad.build_info,
               "wall_s": time.time() - t0, "versions": {"torch": torch.__version__, "python": platform.python_version()}},
              open(out / "run.json", "w"))
    P("METRICS", json.dumps({k: v for k, v in m.items() if k != "config"}))


if __name__ == "__main__":
    main()
