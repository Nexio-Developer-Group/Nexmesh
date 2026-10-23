"""S6-T2: retriever swap (MegaLoc vs DINOv2-small) -> tile recall@1/5 and embed latency. 1 Hz subsample (153 frames).
python t2_retriever.py --retr megaloc|dinov2s --run-id S6-T2-megaloc-20261023-s0"""
import argparse, os, sys, json, time
HERE = os.path.dirname(os.path.abspath(__file__)); ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
sys.path.insert(0, HERE)
import numpy as np, cv2
import s6lib as L
from run_replay import load_truth
ap = argparse.ArgumentParser(); ap.add_argument("--retr"); ap.add_argument("--run-id"); ap.add_argument("--stride", type=int, default=5)
a = ap.parse_args()
D = os.path.join(ROOT, "data", "nidar") + "/"
cfg = dict(L.DEFAULT); mp = cv2.imread(D + "map_esri_z19.png", 0)
ref = L.geo.MapRef(D + "map_esri_z19.json"); mpp = ref.ground_m_per_px(18.5204)
ret = L.make_retriever(a.retr); idx = L.TileIndex(mp, cfg, ret)
tr = load_truth(D + "truth.csv"); t60 = np.genfromtxt(D + "truth60.csv", delimiter=",", names=True)
r1 = r5 = n = 0; lat = []; rows = []
S = cfg["q_crop"]; m = cfg["nu_size"] // 2
for i in range(0, len(tr["t"]), a.stride):
    t = tr["t"][i]; fr = cv2.imread(D + f"frames/{t:.3f}.png", 0); j = np.argmin(abs(t60["t"] - t))
    img, mask, G, c = L.warp_query(fr, np.radians(tr["yaw"][i]), t60["roll"][j], t60["pitch"][j], tr["alt"][i], mpp, cfg["nu_size"])
    nx, ny = ref.latlon_to_px(tr["lat"][i], tr["lon"][i])
    gx, gy = float(nx) + c[0] / mpp, float(ny) - c[1] / mpp          # footprint centre in map px
    crop = cv2.resize(img[m - S // 2:m + S // 2, m - S // 2:m + S // 2], None, fx=cfg["ret_scale"], fy=cfg["ret_scale"], interpolation=cv2.INTER_AREA)
    t0 = time.perf_counter(); q = ret.embed([crop])[0]; lat.append((time.perf_counter() - t0) * 1000)
    ids, _ = idx.search(q, 5)
    ok = [bool(abs(idx.centers[k][0] - gx) <= cfg["tile"] / 2 and abs(idx.centers[k][1] - gy) <= cfg["tile"] / 2) for k in ids]
    r1 += ok[0]; r5 += any(ok); n += 1; rows.append((float(t), ok[0], any(ok)))
out = os.path.join(ROOT, "results", "s6", "nidar", a.run_id); os.makedirs(out, exist_ok=True)
res = dict(retriever=a.retr, frames=n, recall_at_1=100 * r1 / n, recall_at_5=100 * r5 / n, embed_ms_p50=float(np.percentile(lat, 50)), embed_ms_p95=float(np.percentile(lat, 95)),
           index_build_s=idx.build_s, index_mb=idx.size_mb, dim=int(idx.D.shape[1]), criterion="tile (512px) contains footprint centre", note="synthetic NIDAR-like, 1 Hz; latency measured under shared CPU load")
json.dump(res, open(out + "/metrics.json", "w"), indent=1)
open(out + "/log.txt", "w").write(json.dumps(res) + "\n" + "\n".join(map(str, rows)))
print(res)
