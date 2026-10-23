#!/usr/bin/env python
"""Retrieval-only evaluation (CAEVL, no fusion): T2 (5 Hz, T=120), T6 (corridor on/off), T7 (tile-size sweep, 1 Hz).
python retrieval_eval.py --tag S2-T2 --tile 120 --step 1 [--corridors 200,300]"""
import os, sys, json, time, math, argparse
import numpy as np, cv2, pandas as pd
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, HERE)
from s2lib import *
import s2core, run_replay as RR, metrics as MT
from ahrs import complementary_tilt
ap = argparse.ArgumentParser()
ap.add_argument('--tag', required=True); ap.add_argument('--tile', type=float, default=120.0); ap.add_argument('--stride', type=float, default=0.0)
ap.add_argument('--step', type=int, default=1); ap.add_argument('--corridors', default=''); ap.add_argument('--vio', default='')
ap.add_argument('--weights', default=os.path.join(HERE, 'weights/stagetwo.pth')); ap.add_argument('--threads', type=int, default=2)
a = ap.parse_args()
truth = RR.load_truth(os.path.join(ROOT, 'data/nidar/truth.csv'))
d = os.path.join(ROOT, 'results/s2/nidar', f'{a.tag}-20261023-s0'); os.makedirs(d, exist_ok=True)
sys.stdout = open(os.path.join(d, 'log.txt'), 'w')
stride = a.stride or a.tile / 2
R = s2core.Retriever(a.weights, a.tile, stride, threads=a.threads)
print(f'db tiles={len(R.cen)} tile_m={a.tile} stride_m={stride} index_mb={R.index_mb:.2f} build_s={R.build_s:.0f}', flush=True)
img, ref, mpp = load_map()
ti, att = complementary_tilt(os.path.join(ROOT, 'data/nidar/imu.csv'), 0.05)
files = sorted((f for f in os.listdir(os.path.join(ROOT, 'data/nidar/frames')) if f.endswith('.png')), key=lambda f: float(f[:-4]))[::a.step]
vio = pd.read_csv(a.vio) if a.vio else None            # S2-T1 vio_track.csv: VIO-substitute position -> corridor prior
cors = [float(c) for c in a.corridors.split(',') if c]
def en(lat, lon):
    px, py = ref.latlon_to_px(lat, lon); return np.array([px * mpp, -py * mpp])
rows = []; t_enc = []
for f in files:
    t = float(f[:-4]); i = int(np.argmin(np.abs(truth['t'] - t))); k = min(int(np.searchsorted(ti, t)), len(ti) - 1)
    g = cv2.imread(os.path.join(ROOT, 'data/nidar/frames', f), 0)
    crop, (n0, e0) = query_crop_att(g, att[k, 0], att[k, 1], math.radians(truth['yaw'][i]), float(truth['alt'][i]), a.tile)
    s = time.perf_counter(); q = R.encode(crop); t_enc.append((time.perf_counter() - s) * 1000)
    tr = en(truth['lat'][i], truth['lon'][i])
    row = dict(t=t, n0=n0, e0=e0)
    def rec(name, idx, ncand, ms):
        fix = R.cen[idx] - np.array([e0, n0]); row[name + '_err'] = float(np.hypot(*(fix - tr))); row[name + '_ncand'] = ncand; row[name + '_ms'] = ms
        row[name + '_E'] = fix[0]; row[name + '_N'] = fix[1]
    idx, sc, nc, ms = R.search(q); rec('full', idx, nc, np.median([R.search(q)[3] for _ in range(5)])); row['score'] = sc
    # rank of the true tile (nearest tile centre to truth principal point)
    scores = R.desc @ q; gt_c = tr + np.array([e0, n0]); near = np.argmin(np.hypot(R.cen[:, 0] - gt_c[0], R.cen[:, 1] - gt_c[1]))
    row['true_tile_rank'] = int((scores > scores[near]).sum()) + 1
    row['top20_hit100'] = bool((np.hypot(*(R.cen[np.argsort(-scores)[:20]] - gt_c).T) <= 100).any())
    if vio is not None:
        j = int(np.argmin(np.abs(vio.t.values - t))); prior = en(vio.lat.values[j], vio.lon.values[j]) + np.array([e0, n0])
        for c in cors:
            idx, sc, nc, ms = R.search(q, prior, c); rec(f'cor{int(c)}', idx, nc, np.median([R.search(q, prior, c)[3] for _ in range(5)]))
    rows.append(row)
    if len(rows) % 25 == 0: print(len(rows), '/', len(files), f'enc {np.mean(t_enc):.0f} ms', flush=True)
df = pd.DataFrame(rows); df.to_csv(os.path.join(d, 'per_frame.csv'), index=False)
wx = json.load(open(os.path.join(ROOT, 'data/nidar/weather.json')))
def hard(t): return any(b['t0'] <= t <= b['t1'] for b in wx['blur']) or any(h['t0'] <= t <= h['t1'] for h in wx['haze'])
df['hard'] = df.t.map(hard)
def summ(name):
    e = df[name + '_err'].values
    return dict(n=int(len(e)), recall_50=float(100 * np.mean(e <= 50)), recall_100=float(100 * np.mean(e <= 100)), recall_250=float(100 * np.mean(e <= 250)),
                median_err=float(np.median(e)), p95_err=float(np.percentile(e, 95)), mean_err=float(e.mean()),
                recall_100_clear=float(100 * np.mean(e[~df.hard] <= 100)), recall_100_hard=float(100 * np.mean(e[df.hard] <= 100)) if df.hard.any() else None,
                cand_mean=float(df[name + '_ncand'].mean()), search_ms_median=float(df[name + '_ms'].median()))
m = dict(label='synthetic NIDAR-like (map and frames from the same Esri imagery: optimistic)', tile_m=a.tile, stride_m=stride, tiles=int(len(R.cen)), index_mb=R.index_mb,
         db_build_s=R.build_s, step=a.step, encode_ms_mean=float(np.mean(t_enc)), encode_ms_p95=float(np.percentile(t_enc, 95)),
         full=summ('full'), top20_hit100_pct=float(100 * df.top20_hit100.mean()), true_tile_rank_median=float(df.true_tile_rank.median()),
         top1_tile_hit_pct=float(100 * (df.true_tile_rank == 1).mean()))
for c in cors: m[f'corridor_{int(c)}'] = summ(f'cor{int(c)}')
json.dump(m, open(os.path.join(d, 'metrics.json'), 'w'), indent=1); print(json.dumps(m, indent=1))
