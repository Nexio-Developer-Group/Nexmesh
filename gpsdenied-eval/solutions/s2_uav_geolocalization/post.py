#!/usr/bin/env python
"""Post-hoc tests from logged streams (raw CAEVL fixes + VIO) of the T3 run:
  t4: fuse.py CLI as shipped: causal (with its non-causal pre-filter), align, ekf
  t5: consensus rejection off (streaming port with consensus=False) and a determinism check of the streaming port.
python post.py t4|t5 [--src S2-T3-20261023-s0]"""
import os, sys, json, subprocess, argparse, re
import numpy as np, pandas as pd
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, HERE)
from s2lib import *
import s2core, run_replay as RR, metrics as MT
truth = RR.load_truth(os.path.join(ROOT, 'data/nidar/truth.csv'))
ap = argparse.ArgumentParser(); ap.add_argument('cmd'); ap.add_argument('--src', default='S2-T3-20261023-s0'); ap.add_argument('--tag', default='')
a = ap.parse_args()
OUT = os.path.join(ROOT, 'results/s2/nidar'); src = os.path.join(OUT, a.src)
df = pd.read_csv(os.path.join(src, 'extras.csv'))
img, ref, mpp = load_map()
def en(lat, lon):
    px, py = ref.latlon_to_px(np.asarray(lat), np.asarray(lon)); return np.c_[px * mpp, -py * mpp]
def ll(E, N):
    la, lo = ref.px_to_latlon(np.asarray(E) / mpp, -np.asarray(N) / mpp); return la, lo
t = df.t.values; vio = en(df.vio_lat, df.vio_lon); fix = en(df.raw_lat, df.raw_lon); gt = en(truth['lat'], truth['lon'])
gt_i = np.c_[np.interp(t, truth['t'], gt[:, 0]), np.interp(t, truth['t'], gt[:, 1])]
rms = lambda e: float(np.sqrt((e ** 2).mean()))
def score(est_en, name, warm_mask=None):
    live = ~np.isnan(est_en[:, 0]); e = np.hypot(*(est_en - gt_i).T); ev = np.hypot(*(vio - gt_i).T)
    m = dict(name=name, n_live=int(live.sum()), rmse=rms(e[live]), median=float(np.median(e[live])), max=float(e[live].max()), final=float(e[live][-1]),
             rmse_vio_same_frames=rms(ev[live]), gain_vs_vio_pct=float(100 * (1 - rms(e[live]) / rms(ev[live]))))
    return m
def write_run(d, est_en, name, extra):
    os.makedirs(d, exist_ok=True)
    live = ~np.isnan(est_en[:, 0]); la, lo = ll(est_en[live, 0], est_en[live, 1])
    pd.DataFrame(dict(t=t[live], lat=la, lon=lo, alt=np.nan, yaw=np.nan, conf=1.0, accepted=1)).to_csv(os.path.join(d, 'estimates.csv'), index=False)
    est = dict(t=t[live], lat=la, lon=lo, accepted=np.ones(live.sum(), bool)); mm = MT.summarize(est, truth, len(t)); mm.update(extra)
    mm['label'] = 'synthetic NIDAR-like; VIO layer = OUR-VIO-SUBSTITUTE (not OpenVINS)'; json.dump(mm, open(os.path.join(d, 'metrics.json'), 'w'), indent=1); return mm

def cli(mode, extra_args, d):
    os.makedirs(os.path.join(d, 'inputs'), exist_ok=True)
    with open(os.path.join(d, 'inputs/vio.txt'), 'w') as f:
        for i in range(len(t)): f.write(f'{t[i]:.3f} 0 0 0 1 {vio[i,0]:.3f} {vio[i,1]:.3f} 0\n')
    with open(os.path.join(d, 'inputs/fixes.csv'), 'w') as f:
        f.write('frame,timestamp,name,pred_x,pred_y\n')
        for i in range(len(t)):
            s = int(t[i]); fr = int(round((t[i] - s) * 1e6)); f.write(f'{i},{t[i]:.3f},q_{s}_{fr:06d}.png,{fix[i,0]:.3f},{fix[i,1]:.3f}\n')
    gtl = en(truth['lat'], truth['lon'])
    with open(os.path.join(d, 'inputs/gt.csv'), 'w') as f:
        for i in range(len(truth['t'])): f.write(f"{int(round(truth['t'][i] * 1e9))},{gtl[i,0]:.3f},{gtl[i,1]:.3f}\n")
    out = os.path.join(d, 'fused_cli.csv')
    cmd = [sys.executable, os.path.join(HERE, 'repo/tools/fuse.py'), '--vio', os.path.join(d, 'inputs/vio.txt'), '--fixes', os.path.join(d, 'inputs/fixes.csv'),
           '--gt', os.path.join(d, 'inputs/gt.csv'), '--mode', mode, '--out', out] + extra_args
    r = subprocess.run(cmd, capture_output=True, text=True); open(os.path.join(d, 'log.txt'), 'a').write(f'$ {" ".join(cmd)}\n{r.stdout}\n{r.stderr}\n')
    if not os.path.exists(out): return None, r
    o = pd.read_csv(out); full = np.full((len(t), 2), np.nan)
    idx = np.searchsorted(t, o.timestamp.values - 1e-6); full[idx] = o[['x', 'y']].values
    return full, r

if a.cmd == 't4':
    summary = {}
    for mode, args in (('causal', []), ('align', []), ('ekf', [])):
        d = os.path.join(OUT, f'S2-T4-{mode}-20261023-s0'); os.makedirs(d, exist_ok=True)
        open(os.path.join(d, 'log.txt'), 'w').write('')
        est, r = cli(mode, args, d)
        if est is None: summary[mode] = dict(error=(r.stderr or r.stdout)[-400:]); print(mode, 'FAILED', summary[mode]); continue
        m = score(est, f'fuse.py --mode {mode} (batch, as shipped)'); mm = write_run(d, est, mode, m); summary[mode] = m; print(m)
    json.dump(summary, open(os.path.join(OUT, 'S2-T4-summary.json'), 'w'), indent=1)
if a.cmd == 't5':
    res = {}
    for name, kw in (('streaming_consensus_on', dict(consensus=True)), ('streaming_consensus_off', dict(consensus=False))):
        F = s2core.StreamFuser(seed=0, **kw); est = np.full((len(t), 2), np.nan)
        for i in range(len(t)):
            e, info = F.step(t[i], vio[i], fix[i]); est[i] = vio[i] if e is None else e
        fused_from = next((i for i in range(len(t))), 0)
        m = score(est, name); d = os.path.join(OUT, 'S2-T5-' + name.split('_', 1)[1] + '-20261023-s0'); mm = write_run(d, est, name, m)
        # accepted-fix precision on the fixes used for the transform
        e_raw = np.hypot(*(fix - gt_i).T); acc_idx = np.array(sorted(F.ever)); m['n_fix_in_fit'] = int(len(acc_idx)); m['fix_precision_100'] = float(100 * np.mean(e_raw[acc_idx] <= 100)) if len(acc_idx) else None
        m['final_scale'] = F.have[0] if F.have else None; m['final_yaw_deg'] = float(np.degrees(F.have[1])) if F.have else None
        res[name] = m; json.dump(m, open(os.path.join(d, 'streaming_metrics.json'), 'w'), indent=1); print(m)
    # determinism: ON must reproduce the T3 estimates
    ref_e = en(df.lat, df.lon); F = s2core.StreamFuser(seed=0); est = np.zeros((len(t), 2))
    for i in range(len(t)):
        e, _ = F.step(t[i], vio[i], fix[i]); est[i] = vio[i] if e is None else e
    res['determinism_max_diff_m'] = float(np.abs(est - ref_e).max()); print('determinism', res['determinism_max_diff_m'])
    json.dump(res, open(os.path.join(OUT, 'S2-T5-summary.json'), 'w'), indent=1)
if a.cmd == 't9':
    # SANITY / STRESS TEST (synthetic fixes, NOT CAEVL output): does the fusion layer help when fixes are partly right?
    rng = np.random.default_rng(0); res = {}
    vio_sets = {'vio_substitute': vio}
    d1 = os.path.join(OUT, 'S2-T1b-degraded-20261023-s0', 'vio_track.csv')
    if os.path.exists(d1):
        v = pd.read_csv(d1); vio_sets['vio_degraded(3deg yaw bias,-4% scale)'] = en(np.interp(t, v.t, v.lat), np.interp(t, v.t, v.lon))
    for vn, V in vio_sets.items():
        for p in (0.1, 0.2, 0.4):
            fx = gt_i + rng.normal(0, 30, gt_i.shape); bad = rng.random(len(t)) > p
            fx[bad] = np.c_[rng.uniform(img.shape[1] * mpp * 0.2, img.shape[1] * mpp * 0.8, bad.sum()), -rng.uniform(img.shape[0] * mpp * 0.2, img.shape[0] * mpp * 0.8, bad.sum())]
            for cons in (True, False):
                F = s2core.StreamFuser(seed=0, consensus=cons); est = np.zeros((len(t), 2))
                for i in range(len(t)):
                    e, _ = F.step(t[i], V[i], fx[i]); est[i] = V[i] if e is None else e
                e = np.hypot(*(est - gt_i).T); ev = np.hypot(*(V - gt_i).T)
                res[f'{vn}|inlier_frac={p}|consensus={cons}'] = dict(rmse_fused=rms(e), rmse_vio=rms(ev), gain_pct=float(100 * (1 - rms(e) / rms(ev))), final_fused=float(e[-1]), final_vio=float(ev[-1]))
                print(vn, p, cons, res[f'{vn}|inlier_frac={p}|consensus={cons}'])
    os.makedirs(os.path.join(OUT, 'S2-T3x-sanity-20261023-s0'), exist_ok=True)
    json.dump(res, open(os.path.join(OUT, 'S2-T3x-sanity-20261023-s0/metrics.json'), 'w'), indent=1)
