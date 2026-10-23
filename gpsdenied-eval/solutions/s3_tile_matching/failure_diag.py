"""S3 failure analysis on the T1 run: for failed frames report weather window, heading, retrieval rank of the tile containing the truth.
Also saves local-only (git-ignored) montage PNGs: derotated frame | expected map crop | map crop at the pipeline's tile-centre fallback."""
import csv, json, math, os, sys
import numpy as np, cv2, torch
from PIL import Image
HERE = os.path.dirname(os.path.abspath(__file__)); ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
sys.path.insert(0, HERE); sys.path.insert(0, os.path.join(ROOT, "harness"))
import run_replay as R
from adapter import Adapter
run = sys.argv[1] if len(sys.argv) > 1 else "S3-T1-20261023-s0"
rows = list(csv.DictReader(open(os.path.join(ROOT, "results/s3/nidar", run, "estimates_full.csv"))))
truth = R.load_truth(os.path.join(ROOT, "data/nidar/truth.csv")); w = json.load(open(os.path.join(ROOT, "data/nidar/weather.json")))
ad = Adapter(); ad.init(os.path.join(ROOT, "data/nidar/map_esri_z19.png"), {}); T = ad.cfg["tile"]
def win(t):
    for e in w["blur"]:
        if e["t0"] - 1.5 <= t <= e["t1"] + 1.5: return "blur"
    for e in w["haze"]:
        if e["t0"] - 1.5 <= t <= e["t1"] + 1.5: return "haze"
    return "clear"
out = []
os.makedirs(os.path.join(ROOT, "results/s3/figures/failures"), exist_ok=True)
fails = [r for r in rows if r["flag"] != "verified"]
pick = sorted(fails, key=lambda r: float(r["t"]))
sel = []; cnt = {"blur": 0, "haze": 0, "clear": 0}
for r in pick[::3]:
    c = win(float(r["t"]))
    if cnt[c] < 3: sel.append(r); cnt[c] += 1
for r in sel:
    t = float(r["t"]); i = int(np.argmin(abs(truth["t"] - t)))
    X, Y = ad.ref.latlon_to_px(truth["lat"][i], truth["lon"][i]); gx, gy = X * ad.ratio, Y * ad.ratio
    inside = [k for k, (x, y) in enumerate(ad.tiles) if x <= gx <= x + T and y <= gy <= y + T]
    frame = cv2.imread(os.path.join(ROOT, "data/nidar/frames", f"{t:.3f}.png"))
    for derot in (False, True):
        rgb = cv2.cvtColor(frame, cv2.COLOR_BGR2RGB)
        if derot: rgb = ad._derotate(rgb, -float(truth["yaw"][i]))
        with torch.no_grad():
            q = torch.nn.functional.normalize(ad.dino(ad.tf(Image.fromarray(rgb)).unsqueeze(0)), dim=1)
            sims = (q @ ad.emb.T).squeeze(0); order = torch.argsort(sims, descending=True).tolist()
        rank = min(order.index(k) + 1 for k in inside) if inside else None
        if derot: rank_d = rank
        else: rank_n = rank
    out.append(dict(t=t, window=win(t), yaw_deg=round(float(truth["yaw"][i]), 1), top1_sim=round(float(r["top1_sim"]), 3), n_inliers=int(r["n_inliers"]),
                    true_tile_rank_no_derotation=rank_n, true_tile_rank_with_derotation=rank_d, n_tiles=len(ad.tiles)))
    # local-only montage (Esri-derived imagery: not committed)
    px = int(round(T * 0.8)); cx, cy = int(gx), int(gy)
    exp = ad.map[max(cy - 400, 0):cy + 400, max(cx - 500, 0):cx + 500]
    fx, fy = float(r["lon"]), float(r["lat"])
    PX, PY = ad.ref.latlon_to_px(float(r["lat"]), float(r["lon"])); pcx, pcy = int(PX * ad.ratio), int(PY * ad.ratio)
    pred = ad.map[max(pcy - 400, 0):pcy + 400, max(pcx - 500, 0):pcx + 500]
    fr = cv2.resize(ad._derotate(frame, -float(truth["yaw"][i])), (500, 400))
    tiles = [cv2.resize(x, (500, 400)) for x in (exp, pred)]
    cv2.imwrite(os.path.join(ROOT, "results/s3/figures/failures", f"{run}_t{t:06.1f}_{win(t)}.png"), np.hstack([fr] + tiles))
json.dump(out, open(os.path.join(ROOT, "results/s3/nidar", run, "failure_diag.json"), "w"), indent=1)
for o in out: print(o)
