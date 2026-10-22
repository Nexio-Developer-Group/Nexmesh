"""Feed frames + telemetry to an adapter. Modes: offline | realtime. Writes estimates.csv + latency/memory."""
import argparse, csv, importlib.util, json, os, resource, time
import cv2, numpy as np


def load_truth(path):
    rows = list(csv.DictReader(open(path)))
    return {k: np.array([float(r[k]) for r in rows]) for k in ("t", "lat", "lon", "alt", "yaw")}


def load_adapter(path):
    spec = importlib.util.spec_from_file_location("adapter", path)
    m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
    return m.Adapter()


def run(adapter, frames_dir, truth, map_path, out_dir, mode="offline", rate_hz=5.0, hints_on=True, seed=0, config=None):
    os.makedirs(out_dir, exist_ok=True)
    np.random.seed(seed)
    t0 = time.time(); adapter.init(map_path, config or {}); startup = time.time() - t0
    files = sorted(f for f in os.listdir(frames_dir) if f.lower().endswith((".jpg", ".png")))
    times = np.array([float(os.path.splitext(f)[0]) for f in files])
    est, lat_ms, dropped = [], [], 0
    clock0, busy_until = time.time(), 0.0
    for f, t in zip(files, times):
        if mode == "realtime":
            now = time.time() - clock0
            if now < times[0] + (t - times[0]):
                time.sleep(max(0, (t - times[0]) - now))
            if (time.time() - clock0) > (t - times[0]) + 1.0 / rate_hz and busy_until > (t - times[0]):
                dropped += 1; continue
        img = cv2.imread(os.path.join(frames_dir, f))
        hints = None
        if hints_on:
            i = int(np.argmin(np.abs(truth["t"] - t)))
            hints = {"baro_alt": float(truth["alt"][i]), "att": (0.0, 0.0, float(truth["yaw"][i]))}
        s = time.perf_counter(); r = adapter.step(img, float(t), hints); d = time.perf_counter() - s
        busy_until = (t - times[0]) + d
        lat_ms.append(d * 1000); r["t"] = float(t); est.append(r)
    keys = ["t", "lat", "lon", "alt", "yaw", "conf", "accepted"]
    with open(os.path.join(out_dir, "estimates.csv"), "w", newline="") as fh:
        w = csv.DictWriter(fh, keys, extrasaction="ignore"); w.writeheader(); w.writerows(est)
    meta = {"mode": mode, "seed": seed, "startup_s": startup, "frames_total": len(files),
            "frames_processed": len(est), "dropped": dropped,
            "latency_ms": lat_ms, "peak_rss_mb": resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024}
    json.dump(meta, open(os.path.join(out_dir, "run.json"), "w"))
    return est, meta


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--adapter"); ap.add_argument("--frames"); ap.add_argument("--truth")
    ap.add_argument("--map"); ap.add_argument("--out"); ap.add_argument("--mode", default="offline")
    ap.add_argument("--seed", type=int, default=0)
    a = ap.parse_args()
    run(load_adapter(a.adapter), a.frames, load_truth(a.truth), a.map, a.out, a.mode, seed=a.seed)
