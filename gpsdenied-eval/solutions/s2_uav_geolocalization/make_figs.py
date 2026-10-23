import os, sys, json, math, numpy as np, pandas as pd, cv2
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, HERE)
from s2lib import *
import run_replay as RR
import matplotlib; matplotlib.use('Agg'); import matplotlib.pyplot as plt
from ahrs import complementary_tilt
R = os.path.join(ROOT, 'results/s2'); fig = os.path.join(R, 'figures'); os.makedirs(fig + '/failures', exist_ok=True)
img, ref, mpp = load_map(); truth = RR.load_truth(os.path.join(ROOT, 'data/nidar/truth.csv'))
pf = pd.read_csv(os.path.join(R, 'nidar/S2-T2-20261023-s0/per_frame.csv')); ex = pd.read_csv(os.path.join(R, 'nidar/S2-T3-20261023-s0/extras.csv'))
ti, att = complementary_tilt(os.path.join(ROOT, 'data/nidar/imu.csv'), 0.05)
def en(lat, lon): px, py = ref.latlon_to_px(lat, lon); return np.array([px, py])
# trajectory
fig1, ax = plt.subplots(figsize=(7, 7)); 
for n, (la, lo, c) in dict(truth=(truth['lat'], truth['lon'], 'k'), vio=(ex.vio_lat, ex.vio_lon, 'tab:blue'), fused=(ex.lat, ex.lon, 'tab:red')).items():
    p = en(np.asarray(la), np.asarray(lo)); ax.plot(p[0] * mpp, -p[1] * mpp, c, label=n, lw=1.5)
p = en(ex.raw_lat.values, ex.raw_lon.values); ax.scatter(p[0] * mpp, -p[1] * mpp, s=3, c='tab:orange', label='raw CAEVL fix (top-1)')
ax.set_aspect('equal'); ax.legend(); ax.set_xlabel('east (map m)'); ax.set_ylabel('north (map m)'); ax.set_title('S2-T3 synthetic NIDAR-like (VIO = OUR substitute)'); fig1.savefig(fig + '/trajectories.png', dpi=110)
# error vs time
fig2, ax = plt.subplots(figsize=(8, 3.5)); import metrics as MT
for n, (la, lo) in dict(fused=('lat', 'lon'), vio=('vio_lat', 'vio_lon'), raw=('raw_lat', 'raw_lon')).items():
    ax.plot(ex.t, MT.position_errors(dict(t=ex.t.values, lat=ex[la].values, lon=ex[lo].values), truth), label=n)
wx = json.load(open(os.path.join(ROOT, 'data/nidar/weather.json')))
for b in wx['blur']: ax.axvspan(b['t0'], b['t1'], color='gray', alpha=.2)
for h in wx['haze']: ax.axvspan(h['t0'], h['t1'], color='gold', alpha=.15)
ax.set_yscale('log'); ax.set_xlabel('t (s)'); ax.set_ylabel('error (m)'); ax.legend(); fig2.tight_layout(); fig2.savefig(fig + '/error_vs_time.png', dpi=110)
# failure crops
rows = []
for t in (10.0, 24.0, 40.0, 58.0, 74.0, 98.0, 128.0, 145.0):
    i = int(np.argmin(np.abs(pf.t.values - t))); r = pf.iloc[i]; j = int(np.argmin(np.abs(truth['t'] - r.t))); k = min(np.searchsorted(ti, r.t), len(ti) - 1)
    g = cv2.imread(os.path.join(ROOT, f'data/nidar/frames/{r.t:.3f}.png'), 0)
    crop, (n0, e0) = query_crop_att(g, att[k, 0], att[k, 1], math.radians(truth['yaw'][j]), float(truth['alt'][j]), 120)
    tp = en(truth['lat'][j], truth['lon'][j]); cx_t = tp[0] + e0 / mpp; cy_t = tp[1] - n0 / mpp
    exp = cut_tile(img, cx_t, cy_t, 120, mpp)
    fx = r.full_E + e0; fy = r.full_N + n0
    mat = cut_tile(img, fx / mpp, -fy / mpp, 120, mpp)
    cv2.imwrite(fig + f'/failures/t{r.t:07.2f}_query_expected_matched.png', np.hstack([crop, exp, mat]))
    rows.append(dict(t=float(r.t), err_m=float(r.full_err), true_rank=int(r.true_tile_rank), score=float(r.score)))
pd.DataFrame(rows).to_csv(fig + '/failures/index.csv', index=False); print(rows)
# recall by weather window
def tag(t):
    b = any(x['t0'] <= t <= x['t1'] for x in wx['blur']); h = any(x['t0'] <= t <= x['t1'] for x in wx['haze'])
    return 'blur+haze' if b and h else 'blur' if b else 'haze' if h else 'clear'
pf['cond'] = pf.t.map(tag); out = pf.groupby('cond').apply(lambda d: pd.Series(dict(n=len(d), recall_100=100 * (d.full_err <= 100).mean(), median_err=d.full_err.median(), median_true_rank=d.true_tile_rank.median())))
print(out); out.to_csv(fig + '/recall_by_condition.csv')
