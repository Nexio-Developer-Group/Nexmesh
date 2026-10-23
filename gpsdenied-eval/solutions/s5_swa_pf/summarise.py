import json, glob, os, csv, numpy as np
R = os.path.dirname(os.path.abspath(__file__)) + '/../../results/s5/nidar'
def load(run):
    m = json.load(open(f'{R}/{run}/metrics.json')); r = json.load(open(f'{R}/{run}/run.json'))
    ex = list(csv.DictReader(open(f'{R}/{run}/extras.csv'))); tp = np.array([float(x['t_pf']) + float(x['t_seg']) for x in ex]); n = np.array([int(x['n_particles']) for x in ex])
    return m, r, tp, ex
out = []
print('T3 particle sweep (40 frames @1 Hz, nadir, seed 0)')
for n in (1000, 5000, 20000, 40000):
    run = f'S5-T3-n{n}-20261023-s0'
    if not os.path.exists(f'{R}/{run}/metrics.json'): print(n, 'missing'); continue
    m, r, tp, ex = load(run); lat = np.array(r['latency_ms']) / 1000
    print(f"n0={n}: fix_rate {m['fix_rate']:.0f}% median {m['median']} p95 {m['p95']} t_conv_stays50 {m['time_converged_and_stays_50m']} first-step {lat[0]:.1f}s median-step {np.median(lat):.1f}s max {lat.max():.1f}s peakRSS {r['peak_rss_mb']:.0f}MB")
print('T4 init modes (n=5000, 40 frames)')
for init in (0, 1, 2):
    tc, fail = [], 0
    for s in (0, 1, 2):
        run = f'S5-T4-init{init}-20261023-s{s}'
        if not os.path.exists(f'{R}/{run}/metrics.json'): continue
        m = json.load(open(f'{R}/{run}/metrics.json')); t = m['time_converged_and_stays_50m']
        tc.append(t); fail += t is None
    print(f'init {init}: seeds done {len(tc)}, converge+stay<=50m times {tc}, failures (no stable convergence in 40 s) {fail}')
print('T5 resolution')
for res in ('1.0', '2.0'):
    run = f'S5-T5-res{res}-20261023-s0'
    if os.path.exists(f'{R}/{run}/metrics.json'):
        m = json.load(open(f'{R}/{run}/metrics.json')); print(res, {k: m[k] for k in ('fix_rate', 'median', 'p95', 'rmse', 'max', 'time_converged_and_stays_50m')})
