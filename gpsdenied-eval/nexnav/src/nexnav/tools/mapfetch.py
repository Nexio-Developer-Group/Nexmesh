"""Download an XYZ tile mosaic and write PNG + JSON georeference (webmercator_tiles). YOU are responsible for the licence of the tile server
(many imagery providers allow benchmarking only and forbid redistribution)."""
import json, math, os, urllib.request
from concurrent.futures import ThreadPoolExecutor
import numpy as np
import cv2


def fetch(url_template, lat, lon, half_m, z, out):
    mpp = 156543.03392 * math.cos(math.radians(lat)) / 2 ** z
    n = 2 ** z; cx = (lon + 180) / 360 * n; cy = (1 - math.asinh(math.tan(math.radians(lat))) / math.pi) / 2 * n; half = half_m / mpp / 256
    x0, x1, y0, y1 = int(cx - half), int(cx + half), int(cy - half), int(cy + half)
    jobs = [(x, y) for y in range(y0, y1 + 1) for x in range(x0, x1 + 1)]

    def get(j):
        for _ in range(3):
            try: return urllib.request.urlopen(urllib.request.Request(url_template.format(z=z, x=j[0], y=j[1]), headers={"User-Agent": "nexnav/0.1"}), timeout=30).read()
            except Exception: pass
    with ThreadPoolExecutor(16) as ex: data = list(ex.map(get, jobs))
    img = np.zeros(((y1 - y0 + 1) * 256, (x1 - x0 + 1) * 256, 3), np.uint8); miss = 0
    for (x, y), d in zip(jobs, data):
        if d is None: miss += 1; continue
        img[(y - y0) * 256:(y - y0 + 1) * 256, (x - x0) * 256:(x - x0 + 1) * 256] = cv2.imdecode(np.frombuffer(d, np.uint8), cv2.IMREAD_COLOR)
    cv2.imwrite(out, img)
    json.dump({"z": z, "tile_x0": x0, "tile_y0": y0, "width": img.shape[1], "height": img.shape[0], "missing": miss}, open(os.path.splitext(out)[0] + ".json", "w"))
    print(f"wrote {out}: {img.shape[1]}x{img.shape[0]} px, {len(jobs)} tiles, {miss} missing, {mpp:.3f} m/px")
