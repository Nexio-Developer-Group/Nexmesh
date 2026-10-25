"""Render short clear-weather clips of the SAME path at fixed heights above ground (default 100, 300, 500 m) for the altitude check.
Outputs data/alt/<H>m/frames/<t>.png and truth.csv (t, lat, lon, alt, yaw). Same camera (60 deg HFOV, 1280x800), stabilised nadir."""
import csv, json, math, os, sys
import numpy as np, cv2
sys.path.insert(0, os.path.join(os.path.dirname(__file__)))
import render_video as RV

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
heights = [float(h) for h in (sys.argv[1].split(",") if len(sys.argv) > 1 else "100,300,500".split(","))]
step_s = float(sys.argv[2]) if len(sys.argv) > 2 else 5.0
t60 = {k: np.array([float(r[k]) for r in csv.DictReader(open(f"{ROOT}/data/nidar/truth60.csv"))]) for k in ("t", "lat", "lon", "yaw", "roll", "pitch")}
meta = json.load(open(f"{ROOT}/data/nidar/map_esri_z19.json"))
RV.init(f"{ROOT}/data/nidar/map_esri_z19.png", meta, {"blur": [], "haze": []}, stabilised=True)
ts = np.arange(5.0, t60["t"][-1] - 2, step_s)
for H in heights:
    out = f"{ROOT}/data/alt/{int(H)}m"; os.makedirs(out + "/frames", exist_ok=True)
    rows = []
    for k, t in enumerate(ts):
        lat, lon = float(np.interp(t, t60["t"], t60["lat"])), float(np.interp(t, t60["t"], t60["lon"]))
        yaw = math.radians(float(np.interp(t, t60["t"], np.unwrap(np.radians(t60["yaw"])))) if False else 0)
        yawr = float(np.interp(t, t60["t"], np.unwrap(np.radians(t60["yaw"]))))
        _, g, _, _ = RV.render((k, t, lat, lon, 0.0, 0.0, yawr, H))
        cv2.imwrite(f"{out}/frames/{t:.3f}.png", g); rows.append((t, lat, lon, H, math.degrees(yawr) % 360))
    with open(out + "/truth.csv", "w", newline="") as f:
        w = csv.writer(f); w.writerow(["t", "lat", "lon", "alt", "yaw"]); w.writerows(rows)
    print(H, len(rows), "frames")
