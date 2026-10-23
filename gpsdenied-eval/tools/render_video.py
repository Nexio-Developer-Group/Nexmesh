"""Render a synthetic NIDAR-style nadir video from SITL truth over the Esri mosaic.
Camera: 1280x800 (~1 MP) grayscale, global shutter (every frame is one instant, no rolling-shutter skew),
60 fps, 60 deg HFOV pinhole. Weather: scheduled minor blur and close haze episodes.
Outputs (under data/nidar/): video.mp4, frames/<t>.png @5 Hz, truth.csv (5 Hz), truth60.csv, imu.csv,
camera.json, weather.json. Time base: seconds since the start of cruise (arrival at A)."""
import argparse, csv, json, math, os
from multiprocessing import Pool
import numpy as np, cv2

W, H, FPS, HFOV = 1280, 800, 60, 60.0
F = (W / 2) / math.tan(math.radians(HFOV / 2))
EARTH = 6378137.0
ST = {}  # per-process state


def load_flight(path):
    rows = {}
    for line in open(path):
        d = json.loads(line); rows.setdefault(d["mavpackettype"], []).append(d)
    return rows


def build_truth(flight_dir):
    rows = load_flight(os.path.join(flight_dir, "flight.jsonl"))
    arr = json.load(open(os.path.join(flight_dir, "arrivals.json")))["arrive_tboot"]
    sim = rows["SIMSTATE"]
    t = np.array([r["_tboot"] for r in sim]); keep = np.r_[True, np.diff(t) > 1e-6]
    t = t[keep]
    g = lambda k: np.array([r[k] for r in sim])[keep]
    lat, lon = g("lat") / 1e7, g("lng") / 1e7
    roll, pitch, yaw = g("roll"), g("pitch"), np.unwrap(g("yaw"))
    gp = rows["GLOBAL_POSITION_INT"]
    gt = np.array([r["time_boot_ms"] / 1000 for r in gp]); rel = np.array([r["relative_alt"] / 1000 for r in gp])
    t0, t1 = arr["A"], arr["D"]
    tt = np.arange(t0, t1, 1.0 / FPS)
    out = {"t": tt - t0, "lat": np.interp(tt, t, lat), "lon": np.interp(tt, t, lon),
           "roll": np.interp(tt, t, roll), "pitch": np.interp(tt, t, pitch), "yaw": np.interp(tt, t, yaw),
           "alt": np.interp(tt, gt, rel)}
    imu = rows.get("RAW_IMU", [])
    it = np.array([r["_tboot"] for r in imu]); m = (it >= t0) & (it < t1)
    imu_out = [(r["_tboot"] - t0, r["xacc"] / 1000 * 9.80665, r["yacc"] / 1000 * 9.80665, r["zacc"] / 1000 * 9.80665,
                r["xgyro"] / 1000, r["ygyro"] / 1000, r["zgyro"] / 1000) for r, k in zip(imu, m) if k]
    return out, imu_out


def env(t, a, b):  # raised-cosine envelope, 1.5 s fade each side
    r = 1.5
    if t < a - r or t > b + r: return 0.0
    if a <= t <= b: return 1.0
    d = (a - t) if t < a else (t - b)
    return 0.5 * (1 + math.cos(math.pi * d / r))


def weather_at(t, sched):
    blur = sum(e["peak"] * env(t, e["t0"], e["t1"]) for e in sched["blur"])
    haze = max([e["strength"] * env(t, e["t0"], e["t1"]) for e in sched["haze"]] + [0.0])
    return min(blur, 4.0), min(haze, 0.85)


def init(map_path, meta, sched):
    m = cv2.imread(map_path, cv2.IMREAD_GRAYSCALE)
    ST.update(map=m, meta=meta, sched=sched)
    rng = np.random.default_rng(7)
    lf = cv2.resize(rng.random((6, 10)).astype(np.float32), (W * 2, H * 2), interpolation=cv2.INTER_CUBIC)
    ST["haze_field"] = (lf - lf.min()) / (lf.max() - lf.min())
    yy, xx = np.mgrid[0:H, 0:W].astype(np.float32)
    r2 = ((xx - W / 2) ** 2 + (yy - H / 2) ** 2) / (W / 2) ** 2
    ST["vig"] = (1 - 0.18 * r2).astype(np.float32)
    ST["grad"] = (0.6 + 0.4 * (xx / W)).astype(np.float32)


def merc_px(lat, lon, meta):
    n = 2 ** meta["z"]
    x = (lon + 180) / 360 * n
    y = (1 - np.arcsinh(np.tan(np.radians(lat))) / np.pi) / 2 * n
    return (x - meta["tile_x0"]) * 256, (y - meta["tile_y0"]) * 256


def render(args):
    i, t, lat, lon, roll, pitch, yaw, alt = args
    meta, sched = ST["meta"], ST["sched"]
    cr, sr, cp, sp, cy, sy = math.cos(roll), math.sin(roll), math.cos(pitch), math.sin(pitch), math.cos(yaw), math.sin(yaw)
    Rz = np.array([[cy, -sy, 0], [sy, cy, 0], [0, 0, 1]]); Ry = np.array([[cp, 0, sp], [0, 1, 0], [-sp, 0, cp]])
    Rx = np.array([[1, 0, 0], [0, cr, -sr], [0, sr, cr]])
    R = Rz @ Ry @ Rx
    src = np.float32([[0, 0], [W, 0], [W, H], [0, H]]); dst = []
    for u, v in src:
        d = R @ np.array([(H / 2 - v) / F, (u - W / 2) / F, 1.0])  # body x=fwd(up), y=right, z=down
        s = alt / d[2]; north, east = s * d[0], s * d[1]
        la = lat + math.degrees(north / EARTH); lo = lon + math.degrees(east / (EARTH * math.cos(math.radians(lat))))
        dst.append(merc_px(la, lo, meta))
    Hm = cv2.getPerspectiveTransform(src, np.float32(dst))
    img = cv2.warpPerspective(ST["map"], Hm, (W, H), flags=cv2.INTER_LINEAR | cv2.WARP_INVERSE_MAP,
                              borderMode=cv2.BORDER_REPLICATE).astype(np.float32) / 255.0
    img *= ST["vig"]
    blur, haze = weather_at(t, sched)
    if blur > 0.25:
        img = cv2.GaussianBlur(img, (0, 0), blur)
    if haze > 0.02:
        ox, oy = int(30 * t) % W, int(12 * t) % H
        field = ST["haze_field"][oy:oy + H, ox:ox + W]
        tr = 1 - haze * (0.75 + 0.5 * (field - 0.5)) * ST["grad"] * 1.1
        tr = np.clip(tr, 0.12, 1.0)
        img = img * tr + 0.80 * (1 - tr)
    rng = np.random.default_rng(i)
    img = img + rng.normal(0, 0.012, img.shape).astype(np.float32) + rng.normal(0, 1, img.shape).astype(np.float32) * 0.02 * np.sqrt(np.clip(img, 0, 1))
    return i, (np.clip(img, 0, 1) * 255 + 0.5).astype(np.uint8), blur, haze


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--flight", required=True); ap.add_argument("--map", required=True)
    ap.add_argument("--out", required=True); ap.add_argument("--procs", type=int, default=4)
    ap.add_argument("--limit", type=int, default=0)
    a = ap.parse_args()
    os.makedirs(os.path.join(a.out, "frames"), exist_ok=True)
    truth, imu = build_truth(a.flight)
    n = len(truth["t"]); dur = truth["t"][-1]
    sched = {"blur": [{"t0": 0.14 * dur, "t1": 0.20 * dur, "peak": 3.0}, {"t0": 0.62 * dur, "t1": 0.68 * dur, "peak": 2.0}],
             "haze": [{"t0": 0.32 * dur, "t1": 0.45 * dur, "strength": 0.60}, {"t0": 0.62 * dur, "t1": 0.70 * dur, "strength": 0.45},
                      {"t0": 0.82 * dur, "t1": 0.88 * dur, "strength": 0.55}]}
    meta = json.load(open(os.path.splitext(a.map)[0] + ".json"))
    json.dump({"width": W, "height": H, "fps": FPS, "hfov_deg": HFOV, "fx": F, "fy": F, "cx": W / 2, "cy": H / 2,
               "type": "grayscale global-shutter, pinhole, no distortion", "megapixels": W * H / 1e6,
               "agl_m": float(np.median(truth["alt"])), "gsd_m_px": float(np.median(truth["alt"])) / F,
               "map_mpp": meta["mpp_at_centre"], "source_map": meta["source"]}, open(os.path.join(a.out, "camera.json"), "w"), indent=1)
    json.dump(sched, open(os.path.join(a.out, "weather.json"), "w"), indent=1)
    with open(os.path.join(a.out, "truth60.csv"), "w", newline="") as f:
        w = csv.writer(f); w.writerow(["t", "lat", "lon", "alt", "yaw", "roll", "pitch"])
        for k in range(n): w.writerow([f"{truth['t'][k]:.4f}", f"{truth['lat'][k]:.8f}", f"{truth['lon'][k]:.8f}",
                                       f"{truth['alt'][k]:.2f}", f"{math.degrees(truth['yaw'][k]) % 360:.3f}",
                                       f"{truth['roll'][k]:.5f}", f"{truth['pitch'][k]:.5f}"])
    with open(os.path.join(a.out, "truth.csv"), "w", newline="") as f:
        w = csv.writer(f); w.writerow(["t", "lat", "lon", "alt", "yaw"])
        for k in range(0, n, FPS // 5): w.writerow([f"{truth['t'][k]:.3f}", f"{truth['lat'][k]:.8f}", f"{truth['lon'][k]:.8f}",
                                                    f"{truth['alt'][k]:.2f}", f"{math.degrees(truth['yaw'][k]) % 360:.3f}"])
    with open(os.path.join(a.out, "imu.csv"), "w", newline="") as f:
        w = csv.writer(f); w.writerow(["t", "ax", "ay", "az", "gx", "gy", "gz"]); w.writerows(imu)
    idx = range(n if not a.limit else min(n, a.limit))
    jobs = [(k, truth["t"][k], truth["lat"][k], truth["lon"][k], truth["roll"][k], truth["pitch"][k], truth["yaw"][k], truth["alt"][k]) for k in idx]
    vw = cv2.VideoWriter(os.path.join(a.out, "video.mp4"), cv2.VideoWriter_fourcc(*"mp4v"), FPS, (W, H), True)
    wlog = []
    with Pool(a.procs, initializer=init, initargs=(a.map, meta, sched)) as p:
        for i, g, b, hz in p.imap(render, jobs, chunksize=8):
            vw.write(cv2.cvtColor(g, cv2.COLOR_GRAY2BGR))
            if i % (FPS // 5) == 0: cv2.imwrite(os.path.join(a.out, "frames", f"{truth['t'][i]:.3f}.png"), g)
            wlog.append((round(float(truth["t"][i]), 3), round(b, 3), round(hz, 3)))
            if i % 600 == 0: print("frame", i, "/", n, flush=True)
    vw.release()
    json.dump({"t_blur_haze": wlog[::6]}, open(os.path.join(a.out, "weather_log.json"), "w"))
    print("frames", len(wlog), "duration_s", round(dur, 1))


if __name__ == "__main__":
    main()
