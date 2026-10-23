"""Download an Esri World_Imagery mosaic around a centre and save a metric GeoTIFF-like PNG + JSON georef.
Benchmark use only (Esri terms). Web-Mercator, so pixel size is constant in mercator metres."""
import argparse, json, math, os, urllib.request
import numpy as np, cv2
from concurrent.futures import ThreadPoolExecutor

URL = "https://server.arcgisonline.com/ArcGIS/rest/services/World_Imagery/MapServer/tile/{z}/{y}/{x}"


def deg2tile(lat, lon, z):
    n = 2 ** z
    return (lon + 180) / 360 * n, (1 - math.asinh(math.tan(math.radians(lat))) / math.pi) / 2 * n


def fetch(args):
    z, x, y = args
    for _ in range(3):
        try:
            req = urllib.request.Request(URL.format(z=z, x=x, y=y), headers={"User-Agent": "gpsdenied-eval/0.1"})
            return urllib.request.urlopen(req, timeout=30).read()
        except Exception:
            pass
    return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--lat", type=float); ap.add_argument("--lon", type=float)
    ap.add_argument("--half_m", type=float, default=900); ap.add_argument("--z", type=int, default=19)
    ap.add_argument("--out", required=True)
    a = ap.parse_args()
    mpp = 156543.03392 * math.cos(math.radians(a.lat)) / 2 ** a.z  # metres/px at centre latitude
    half_px = a.half_m / mpp
    cx, cy = deg2tile(a.lat, a.lon, a.z)
    x0, x1 = int(cx - half_px / 256), int(cx + half_px / 256)
    y0, y1 = int(cy - half_px / 256), int(cy + half_px / 256)
    jobs = [(a.z, x, y) for y in range(y0, y1 + 1) for x in range(x0, x1 + 1)]
    with ThreadPoolExecutor(16) as ex:
        data = list(ex.map(fetch, jobs))
    W, H = (x1 - x0 + 1) * 256, (y1 - y0 + 1) * 256
    mosaic = np.zeros((H, W, 3), np.uint8); miss = 0
    for (z, x, y), d in zip(jobs, data):
        if d is None: miss += 1; continue
        t = cv2.imdecode(np.frombuffer(d, np.uint8), cv2.IMREAD_COLOR)
        mosaic[(y - y0) * 256:(y - y0 + 1) * 256, (x - x0) * 256:(x - x0 + 1) * 256] = t
    os.makedirs(os.path.dirname(a.out) or ".", exist_ok=True)
    cv2.imwrite(a.out, mosaic)
    json.dump({"z": a.z, "tile_x0": x0, "tile_y0": y0, "width": W, "height": H, "tiles": len(jobs), "missing": miss,
               "mpp_at_centre": mpp, "centre": [a.lat, a.lon], "source": "Esri World_Imagery (benchmark only)"},
              open(os.path.splitext(a.out)[0] + ".json", "w"), indent=1)
    print(f"{W}x{H}px, {len(jobs)} tiles, {miss} missing, {mpp:.3f} m/px")


if __name__ == "__main__":
    main()
