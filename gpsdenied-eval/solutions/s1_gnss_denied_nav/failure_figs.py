"""Failure figures for S1: frame (north-up query), expected map crop at truth, top-1 retrieved tile + rank of the true tile.
Usage: env/bin/python solutions/s1_gnss_denied_nav/failure_figs.py t1 t2 ...   -> results/s1/figures/failures/t<T>.png + failures.json"""
import sys, json, csv
from pathlib import Path
import cv2, numpy as np, torch
HERE = Path(__file__).resolve().parent; ROOT = HERE.parents[1]
sys.path.insert(0, str(HERE)); sys.path.insert(0, str(ROOT / "harness"))
import adapter as A
from metrics import haversine
from gdnav.query import make_query
from gdnav.embed import embed_images
torch.set_grad_enabled(False)
ad = A.Adapter(); ad.init(str(ROOT / "data/nidar/map_esri_z19.png"), {"query_kp": 1024})
loc = ad.loc; out = ROOT / "results/s1/figures/failures"; out.mkdir(parents=True, exist_ok=True)
truth = list(csv.DictReader(open(ROOT / "data/nidar/truth.csv")))
rows = {}
for t in map(float, sys.argv[1:]):
    i = int(round(t / 0.2)); tr = truth[i]
    fr = cv2.imread(str(ROOT / f"data/nidar/frames/{t:.3f}.png")); rgb = cv2.cvtColor(fr, cv2.COLOR_BGR2RGB)
    q = make_query(rgb, float(tr["alt"]), float(tr["yaw"]), ad.cam, 224, ad.cfg["patch_m"])
    e = torch.from_numpy(embed_images(loc.model, [q], "cpu"))[0]
    sim = loc.db @ e; top = sim.topk(10).indices.numpy()
    d_true = haversine(float(tr["lat"]), float(tr["lon"]), loc.tiles_ll[:, 0], loc.tiles_ll[:, 1])
    true_tile = int(np.argmin(d_true)); rank = int((sim > sim[true_tile]).sum()) + 1
    d_top = [float(haversine(float(tr["lat"]), float(tr["lon"]), *loc.tiles_ll[k])) for k in top]
    crop = lambda k: cv2.resize(loc._crop(*loc.centers[k]), (224, 224))
    exp = crop(true_tile); t1 = crop(top[0])
    fig = np.concatenate([q, exp, t1], 1)[:, :, ::-1].copy()
    for j, s in enumerate(["query (north-up)", f"expected tile (true rank {rank}/{len(sim)})", f"top-1 retrieved ({d_top[0]:.0f} m off)"]):
        cv2.putText(fig, s, (4 + 224 * j, 14), cv2.FONT_HERSHEY_SIMPLEX, 0.4, (0, 255, 255), 1)
    cv2.imwrite(str(out / f"t{t:.0f}.png"), fig)
    rows[f"{t:.1f}"] = dict(true_tile_rank=rank, top10_min_dist_m=min(d_top), top1_dist_m=d_top[0], sim_true=float(sim[true_tile]), sim_top1=float(sim[top[0]]))
    print(t, rows[f"{t:.1f}"], flush=True)
json.dump(rows, open(out / "failures.json", "w"), indent=1)
