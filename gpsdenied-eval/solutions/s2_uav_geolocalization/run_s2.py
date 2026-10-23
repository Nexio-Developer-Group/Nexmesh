#!/usr/bin/env python
"""Drivers for S2 tests.  Usage: python run_s2.py <t1|t2|t3|post|t6|t7|t8> [args].  Uses harness/run_replay.run() and harness/metrics.summarize()."""
import os, sys, json, csv, time, math, argparse, subprocess, shutil
import numpy as np, cv2, pandas as pd
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, HERE)
from s2lib import *
import run_replay as RR, metrics as MT
DATE = '20261023'
FR = os.path.join(ROOT, 'data/nidar/frames'); TR = os.path.join(ROOT, 'data/nidar/truth.csv')
OUT = os.path.join(ROOT, 'results/s2/nidar')
truth = RR.load_truth(TR)

def run_dir(tid, seed=0, sub=''):
    d = os.path.join(OUT, f'{tid}-{DATE}-s{seed}{sub}'); os.makedirs(d, exist_ok=True); return d

def load_adapter(config):
    spec = importlib.util.spec_from_file_location('s2_adapter', os.path.join(HERE, 'adapter.py'))
    m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m); a = m.Adapter(); return a
import importlib.util

def to_est(rows, latk='lat', lonk='lon', acc=None):
    return {'t': np.array([r['t'] for r in rows]), 'lat': np.array([r[latk] for r in rows]), 'lon': np.array([r[lonk] for r in rows]),
            'accepted': np.array([True if acc is None else acc(r) for r in rows])}

def path_len():
    return MT.path_length(truth['lat'], truth['lon'])

def errs(est):
    return MT.position_errors(est, truth)

def write_est(d, est, extra_keys=None):
    with open(os.path.join(d, 'estimates.csv'), 'w', newline='') as fh:
        w = csv.writer(fh); w.writerow(['t', 'lat', 'lon', 'alt', 'yaw', 'conf', 'accepted'])
        for i in range(len(est['t'])): w.writerow([f"{est['t'][i]:.3f}", f"{est['lat'][i]:.8f}", f"{est['lon'][i]:.8f}", '', '', 1.0, int(est['accepted'][i])])

def save_metrics(d, est, n_frames, extra=None):
    m = MT.summarize(est, truth, n_frames); m.update(extra or {}); m['label'] = 'synthetic NIDAR-like'
    json.dump(m, open(os.path.join(d, 'metrics.json'), 'w'), indent=1); return m

def drift_stats(est):
    e = errs(est); L = path_len(); d_t = np.r_[0, np.cumsum(MT.haversine(truth['lat'][:-1], truth['lon'][:-1], truth['lat'][1:], truth['lon'][1:]))]
    d_at = np.interp(est['t'], truth['t'], d_t)
    slope = float(np.polyfit(d_at / 1000.0, e, 1)[0])           # m of error growth per km (linear fit)
    return dict(final_err_m=float(e[-1]), path_m=L, drift_m_per_km=float(e[-1] / (L / 1000)), drift_slope_m_per_km=slope,
                drift_pct_of_path=float(100 * e[-1] / L), rmse=float(np.sqrt((e ** 2).mean())))

def cmd_t1(a):
    """VIO only (OUR-VIO-SUBSTITUTE). No retrieval, no fusion."""
    from ahrs import complementary_tilt
    from vio_sub import VioSubstitute
    d = run_dir(a.tag or 'S2-T1'); log = open(os.path.join(d, 'log.txt'), 'w')
    ref = MapRef(MAP_JSON); mpp = ref.ground_m_per_px(18.52)
    ti, att = complementary_tilt(os.path.join(ROOT, 'data/nidar/imu.csv'), 0.05)
    v = VioSubstitute(yaw_bias_deg=a.yaw_bias, scale_err=a.scale_err)
    lat0, lon0 = 18.51770510, 73.85385750
    px0, py0 = ref.latlon_to_px(lat0, lon0)
    rows = []; t0 = time.time(); lat_ms = []
    files = sorted((f for f in os.listdir(FR) if f.endswith('.png')), key=lambda f: float(f[:-4]))
    for f in files:
        t = float(f[:-4]); i = int(np.argmin(np.abs(truth['t'] - t)))
        g = cv2.imread(os.path.join(FR, f), 0); k = min(np.searchsorted(ti, t), len(ti) - 1)
        s = time.perf_counter()
        p, resp = v.step(g, t, att[k, 0], att[k, 1], math.radians(truth['yaw'][i]), float(truth['alt'][i]))
        lat_ms.append((time.perf_counter() - s) * 1000)
        la, lo = ref.px_to_latlon(px0 + p[0] / mpp, py0 - p[1] / mpp)
        rows.append(dict(t=t, lat=float(la), lon=float(lo), resp=resp))
    est = to_est(rows)
    write_est(d, est)
    m = save_metrics(d, est, len(files), drift_stats(est)); m.update(lost_frames=v.n_lost, wall_s=time.time() - t0, ms_per_frame_mean=float(np.mean(lat_ms)),
                                                                   label='synthetic NIDAR-like; OUR-VIO-SUBSTITUTE, NOT OpenVINS', yaw_bias_deg=a.yaw_bias, scale_err=a.scale_err)
    json.dump(m, open(os.path.join(d, 'metrics.json'), 'w'), indent=1)
    json.dump(dict(latency_ms=lat_ms, frames=len(files)), open(os.path.join(d, 'run.json'), 'w'))
    pd.DataFrame(rows).to_csv(os.path.join(d, 'vio_track.csv'), index=False)
    print(json.dumps(m, indent=1), file=log); print(json.dumps(m, indent=1)); log.close()


import resource

def frames_list(step=1):
    files = sorted((f for f in os.listdir(FR) if f.endswith('.png')), key=lambda f: float(f[:-4]))
    return files[::step]

def hints_at(t):
    i = int(np.argmin(np.abs(truth['t'] - t)))
    return {'baro_alt': float(truth['alt'][i]), 'att': (0.0, 0.0, float(truth['yaw'][i]))}     # identical to harness/run_replay.py

def run_adapter(d, config, step=1, mode='offline', rate_hz=5.0, label=''):
    """Replay frames (numeric time order) through adapter.step(); same hint construction as harness/run_replay.py."""
    os.makedirs(d, exist_ok=True)
    A = load_adapter(config); t0 = time.time(); A.init(os.path.join(ROOT, 'data/nidar/map_esri_z19.png'), config); startup = time.time() - t0
    files = frames_list(step); rows = []; lat_ms = []; dropped = 0
    clock0 = time.time(); busy_until = 0.0; tfirst = float(files[0][:-4])
    for f in files:
        t = float(f[:-4])
        if mode == 'realtime':
            now = time.time() - clock0
            if now < t - tfirst: time.sleep(max(0, (t - tfirst) - now))
            if (time.time() - clock0) > (t - tfirst) + 1.0 / rate_hz and busy_until > (t - tfirst): dropped += 1; continue
        img = cv2.imread(os.path.join(FR, f))
        s = time.perf_counter(); r = A.step(img, t, hints_at(t)); dt = time.perf_counter() - s
        busy_until = (t - tfirst) + dt; lat_ms.append(dt * 1000); r['t'] = t; rows.append(r)
    ever = A.fuser.ever
    for i, r in enumerate(rows): r['fix_ever_ok'] = (i in ever)
    pd.DataFrame(rows).to_csv(os.path.join(d, 'extras.csv'), index=False)
    with open(os.path.join(d, 'estimates.csv'), 'w', newline='') as fh:
        w = csv.writer(fh); w.writerow(['t', 'lat', 'lon', 'alt', 'yaw', 'conf', 'accepted'])
        for r in rows: w.writerow([f"{r['t']:.3f}", f"{r['lat']:.8f}", f"{r['lon']:.8f}", f"{r['alt']:.2f}", f"{r['yaw']:.2f}", f"{r['conf']:.3f}", int(r['accepted'])])
    json.dump(dict(mode=mode, step=step, startup_s=startup, frames_total=len(files), frames_processed=len(rows), dropped=dropped, latency_ms=lat_ms,
                   peak_rss_mb=resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024, config={k: str(v) for k, v in config.items()},
                   db=dict(path=A.R.db_path, tiles=int(len(A.R.cen)), tile_m=A.R.tile_m, stride_m=A.R.stride_m, index_mb=A.R.index_mb, build_s=A.R.build_s)),
              open(os.path.join(d, 'run.json'), 'w'))
    return pd.DataFrame(rows)

def stream_metrics(df, step=1):
    """Metrics (harness summarize) for each stream; fused vs VIO-alone vs raw retrieval."""
    out = {}
    n = len(df); t = df.t.values
    for name, (la, lo) in dict(fused=('lat', 'lon'), raw=('raw_lat', 'raw_lon'), vio=('vio_lat', 'vio_lon')).items():
        est = dict(t=t, lat=df[la].values, lon=df[lo].values, accepted=np.ones(n, bool))
        m = MT.summarize(est, truth, n); e = MT.position_errors(est, truth)
        m['final_err'] = float(e[-1]); out[name] = m
    fused_live = (df.flag == 'fused').values
    if fused_live.any():
        e_f = MT.position_errors(dict(t=t, lat=df.lat.values, lon=df.lon.values), truth); e_v = MT.position_errors(dict(t=t, lat=df.vio_lat.values, lon=df.vio_lon.values), truth)
        r = lambda e: float(np.sqrt((e ** 2).mean()))
        out['post_warmup'] = dict(frames=int(fused_live.sum()), t_start=float(t[fused_live][0]), rmse_fused=r(e_f[fused_live]), rmse_vio=r(e_v[fused_live]),
                                  rmse_gain_pct=float(100 * (1 - r(e_f[fused_live]) / r(e_v[fused_live]))), median_fused=float(np.median(e_f[fused_live])), median_vio=float(np.median(e_v[fused_live])),
                                  final_fused=float(e_f[-1]), final_vio=float(e_v[-1]), max_fused=float(e_f[fused_live].max()), max_vio=float(e_v[fused_live].max()))
        out['whole_flight'] = dict(rmse_fused=r(e_f), rmse_vio=r(e_v), rmse_gain_pct=float(100 * (1 - r(e_f) / r(e_v))))
    e_raw = MT.position_errors(dict(t=t, lat=df.raw_lat.values, lon=df.raw_lon.values), truth)
    ok = df.fix_arrival_ok.fillna(False).astype(bool).values
    out['fix_acceptance'] = dict(n_fixes=int(n), raw_precision_100=float(100 * np.mean(e_raw <= 100)), arrival_accepted=int(ok.sum()),
                                 arrival_precision_100=float(100 * np.mean(e_raw[ok] <= 100)) if ok.any() else None,
                                 arrival_recall_of_good=float(100 * (ok & (e_raw <= 100)).sum() / max((e_raw <= 100).sum(), 1)),
                                 ever_accepted=int(df.fix_ever_ok.sum()) if 'fix_ever_ok' in df else None,
                                 ever_precision_100=float(100 * np.mean(e_raw[df.fix_ever_ok.values.astype(bool)] <= 100)) if 'fix_ever_ok' in df and df.fix_ever_ok.any() else None)
    return out

def base_config(a):
    c = dict(tile_m=a.tile, stride_m=a.stride or a.tile / 2, seed=0, weights=os.path.join(HERE, 'weights/stagetwo.pth'))
    if a.corridor: c['corridor_m'] = a.corridor
    if a.no_consensus: c['consensus'] = False
    if a.yaw_bias: c['vio_yaw_bias_deg'] = a.yaw_bias
    return c

def cmd_t3(a):
    """Full 5 Hz (or --step N) run of the adapter: VIO substitute + CAEVL + fuse.py causal."""
    tid = a.tag or 'S2-T3'; d = run_dir(tid)
    sys.stdout = Tee(os.path.join(d, 'log.txt'))
    cfg = base_config(a); print('config', cfg)
    df = run_adapter(d, cfg, step=a.step)
    m = stream_metrics(df); m['label'] = 'synthetic NIDAR-like; VIO layer = OUR-VIO-SUBSTITUTE (not OpenVINS)'; m['config'] = {k: str(v) for k, v in cfg.items()}
    json.dump(m, open(os.path.join(d, 'metrics.json'), 'w'), indent=1); print(json.dumps(m, indent=1))

class Tee:
    def __init__(self, p): self.f = open(p, 'w'); self.o = sys.__stdout__
    def write(self, s): self.f.write(s); self.o.write(s); self.f.flush()
    def flush(self): self.f.flush(); self.o.flush()

def cmd_t8(a):
    """Real-time / constrained replay driver. NOT run by the S2 agent (lead runs alone).
    realtime:    python run_s2.py t8 --mode realtime
    constrained: python run_s2.py t8 --mode constrained --cores 0 --threads 1   (1 core pinned, 1 torch thread)"""
    if a.cores is not None: os.sched_setaffinity(0, {int(c) for c in a.cores.split(',')})
    import torch; torch.set_num_threads(a.threads)
    tid = f'S2-T8-{a.mode}'; d = run_dir(tid); sys.stdout = Tee(os.path.join(d, 'log.txt'))
    cfg = base_config(a); cfg['threads'] = a.threads
    df = run_adapter(d, cfg, step=1, mode='realtime', rate_hz=5.0)
    m = stream_metrics(df); lat = np.array(json.load(open(os.path.join(d, 'run.json')))['latency_ms'])
    m['latency_ms'] = dict(p50=float(np.percentile(lat, 50)), p95=float(np.percentile(lat, 95)), mean=float(lat.mean()), achieved_hz=float(1000 / lat.mean()))
    json.dump(m, open(os.path.join(d, 'metrics.json'), 'w'), indent=1); print(json.dumps(m['latency_ms']))

if __name__ == '__main__':
    ap = argparse.ArgumentParser(); ap.add_argument('cmd'); ap.add_argument('--yaw-bias', type=float, default=0.0); ap.add_argument('--scale-err', type=float, default=0.0)
    ap.add_argument('--tag', default=''); ap.add_argument('--tile', type=float, default=120.0); ap.add_argument('--stride', type=float, default=0.0)
    ap.add_argument('--corridor', type=float, default=0.0); ap.add_argument('--no-consensus', action='store_true'); ap.add_argument('--step', type=int, default=1)
    ap.add_argument('--mode', default='realtime'); ap.add_argument('--cores', default=None); ap.add_argument('--threads', type=int, default=2)
    a = ap.parse_args()
    globals()['cmd_' + a.cmd](a)
