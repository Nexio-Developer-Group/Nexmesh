"""S7 item 2: Bearing-UAV inference on SYNTHETIC NIDAR-like frames (not the paper's data).
Input format of the released model: 4 satellite tiles (2x2 block, 256px, 64 m each @254k scale) + 1 UAV patch (256px, 64 m).
Output: position inside the block (units of 32 m from block centre, x east / y south) and heading vector (cos,sin).
Assumptions (UNVERIFIED, from reading repo code + 3 dataset samples): tile order p1=(-1,-1) p2=(-1,+1) p3=(+1,-1) p4=(+1,+1) in (x,y) units, contiguous tiles.
The block centre is placed at truth + seeded random offset (oracle prior, as in the paper's 'known block' setting)."""
import sys, os, json, time, types, csv, argparse
import numpy as np, cv2, torch
from PIL import Image
torch.set_num_threads(2)
HERE = os.path.dirname(os.path.abspath(__file__)); ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
REPO = os.path.join(HERE, "clones", "bearinguav"); sys.path.insert(0, REPO); sys.path.insert(0, os.path.join(ROOT, "harness"))
for m in ["imgaug", "imgaug.augmenters", "albumentations"]:  # augmentation libs unused at inference; stub to avoid installing
    sys.modules[m] = types.ModuleType(m)
sys.modules["imgaug"].augmenters = sys.modules["imgaug.augmenters"]
from cvphr.models.posaglreg.models import PARCASGM_v5a
from torchvision import transforms
import metrics, geo, run_replay
TF = transforms.Compose([transforms.ToTensor(), transforms.Normalize([0.485, 0.456, 0.406], [0.229, 0.224, 0.225])])
UNIT_M = 32.0  # 128 px * 0.25 m
def load_model():
    d = os.path.join(HERE, "weights", "Bearing_UAV", "cross_view"); cfg = json.load(open(d + "/training_configure.json"))
    m = PARCASGM_v5a(**cfg["model_kwargs"]); ck = torch.load(d + "/best_model.pth", map_location="cpu")
    m.load_state_dict(ck["model_state_dict"]); return m.eval()
def to_t(bgr256): return TF(Image.fromarray(cv2.cvtColor(bgr256, cv2.COLOR_BGR2RGB)))
@torch.no_grad()
def infer(model, tiles, uav):
    x = torch.stack([to_t(t) for t in tiles] + [to_t(uav)])[None]
    p, d = model(x); return p[0].numpy(), d[0].numpy()
def sample_mode(model, out):
    import pandas as pd
    ex = "/tmp/claude-0/s7probe/ex/"; df = pd.read_csv("/tmp/claude-0/s7probe/meta.csv"); rows = []
    for i in [0, 2, 5]:
        r = df.iloc[i]; ld = lambda c: cv2.imread(ex + r[c].replace("./", "", 1))
        p, d = infer(model, [ld(c) for c in ["p1_path", "p2_path", "p3_path", "p4_path"]], ld("target_path"))
        gt = np.array([r.x_norm, r.y_norm]); ang = np.degrees(np.arccos(np.clip(d @ [r.x_cosa, r.y_sina] / (np.linalg.norm(d) + 1e-8), -1, 1)))
        rows.append(dict(sample=int(i), pred_x=float(p[0]), pred_y=float(p[1]), gt_x=float(gt[0]), gt_y=float(gt[1]),
                         pos_err_m=float(np.linalg.norm(p - gt) * UNIT_M), head_err_deg=float(ang)))
    json.dump(rows, open(out, "w"), indent=1); print(rows)
def nidar_mode(model, step, out, seed=0, amp=0.9):
    rng = np.random.RandomState(seed)
    mp = os.path.join(ROOT, "data/nidar/map_esri_z19.png"); big = cv2.imread(mp); ref = geo.MapRef(mp.replace(".png", ".json"))
    tr = run_replay.load_truth(os.path.join(ROOT, "data/nidar/truth.csv"))
    fr = sorted(os.listdir(os.path.join(ROOT, "data/nidar/frames"))); ts = np.array([float(f[:-4]) for f in fr])
    idx = np.arange(0, len(fr), step); rows = []; lat_ms = []
    for k in idx:
        t = ts[k]; im = cv2.imread(os.path.join(ROOT, "data/nidar/frames", fr[k]))
        lat, lon, yaw = [np.interp(t, tr["t"], tr[c]) for c in ("lat", "lon", "yaw")]
        mpp = ref.ground_m_per_px(lat); cx, cy = ref.latlon_to_px(lat, lon)
        off = rng.uniform(-amp, amp, 2)  # truth relative to block centre = -off ... block centre = truth - off*unit
        bx, by = cx - off[0] * UNIT_M / mpp, cy - off[1] * UNIT_M / mpp  # block centre px
        half = 32.0 / mpp; tiles = []
        for (sx, sy) in [(-1, -1), (-1, 1), (1, -1), (1, 1)]:
            tcx, tcy = bx + sx * UNIT_M / mpp, by + sy * UNIT_M / mpp
            t0 = cv2.getRectSubPix(big, (int(round(2 * half)),) * 2, (float(tcx), float(tcy)))
            tiles.append(cv2.resize(t0, (256, 256), interpolation=cv2.INTER_AREA))
        gsd = 0.18; side = int(round(64.0 / gsd)); h, w = im.shape[:2]
        crop = im[(h - side) // 2:(h + side) // 2, (w - side) // 2:(w + side) // 2]
        uav = cv2.resize(crop, (256, 256), interpolation=cv2.INTER_AREA)
        s = time.perf_counter(); p, d = infer(model, tiles, uav); lat_ms.append((time.perf_counter() - s) * 1000)
        plat, plon = ref.px_to_latlon(bx + p[0] * UNIT_M / mpp, by + p[1] * UNIT_M / mpp)
        blat, blon = ref.px_to_latlon(bx, by)
        # heading: pred (cos,sin) in image coords (x east,y south) -> compass; truth: image-up = yaw -> (sin yaw, -cos yaw)  [ASSUMED convention]
        pred_yaw = np.degrees(np.arctan2(d[0], -d[1])) % 360
        he = abs((pred_yaw - yaw + 180) % 360 - 180)
        rows.append(dict(t=t, lat=plat, lon=plon, alt=0, yaw=pred_yaw, conf=1.0, accepted=True, blk_lat=blat, blk_lon=blon,
                         truth_yaw=yaw, head_err_deg=he, blk_err_m=float(metrics.haversine(lat, lon, blat, blon))))
    os.makedirs(out, exist_ok=True)
    keys = list(rows[0]); csv.DictWriter(open(out + "/estimates.csv", "w", newline=""), keys).writeheader()
    w = csv.DictWriter(open(out + "/estimates.csv", "w", newline=""), keys); w.writeheader(); w.writerows(rows)
    est = {k: np.array([r[k] for r in rows]) for k in ["t", "lat", "lon", "accepted"]}
    m = metrics.summarize(est, tr, len(rows)); he = np.array([r["head_err_deg"] for r in rows])
    m["heading_err_median_deg"] = float(np.median(he)); m["heading_err_mean_deg"] = float(he.mean())
    m["baseline_block_centre_err_median_m"] = float(np.median([r["blk_err_m"] for r in rows]))
    m["baseline_block_centre_err_mean_m"] = float(np.mean([r["blk_err_m"] for r in rows]))
    m["baseline_random_heading_expected_deg"] = 90.0
    m["label"] = "synthetic NIDAR-like; oracle block prior within +-%.1f units (32 m); %d frames (step %d); seed %d" % (amp, len(rows), step, seed)
    m["latency_ms_median"] = float(np.median(lat_ms)); json.dump(m, open(out + "/metrics.json", "w"), indent=1)
    json.dump(dict(latency_ms=lat_ms, threads=2), open(out + "/run.json", "w")); print(json.dumps(m, indent=1))
if __name__ == "__main__":
    ap = argparse.ArgumentParser(); ap.add_argument("mode"); ap.add_argument("--out"); ap.add_argument("--step", type=int, default=5); ap.add_argument("--seed", type=int, default=0)
    a = ap.parse_args(); mdl = load_model()
    sample_mode(mdl, a.out) if a.mode == "sample" else nidar_mode(mdl, a.step, a.out, a.seed)
