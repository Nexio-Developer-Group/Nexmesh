"""Driver: replays NIDAR frames through the S5 adapter with noisy telemetry odometry (NOT ground-truth poses). Writes results/s5/nidar/<run-id>/."""
import argparse, csv, json, os, sys, time, resource
import numpy as np, cv2
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, HERE); sys.path.insert(0, HERE + '/../../harness')
import run_replay, metrics, importlib
D = HERE + '/../../data/nidar'
ap = argparse.ArgumentParser()
ap.add_argument('--run-id', required=True); ap.add_argument('--stride', type=int, default=5)   # 5 -> 1 Hz, 1 -> 5 Hz
ap.add_argument('--n', type=int, default=5000); ap.add_argument('--init', type=int, default=0); ap.add_argument('--res', type=float, default=1.0)
ap.add_argument('--seed', type=int, default=0); ap.add_argument('--max-frames', type=int, default=0); ap.add_argument('--t-start', type=float, default=0)
ap.add_argument('--sig-v', type=float, default=0.5); ap.add_argument('--sig-yaw', type=float, default=1.5)
ap.add_argument('--realtime', action='store_true', help='T7: frames arrive on a wall clock (5 Hz when --stride 1); frames arriving while the filter is busy are dropped')
ap.add_argument('--rectify', action='store_true', help='EXTENSION: ortho-rectify frames with IMU roll/pitch + baro height (noise 0.5 deg / 1 m)')
ap.add_argument('--no-odo', action='store_true'); ap.add_argument('--label', default=None); ap.add_argument('--out-root', default=HERE + '/../../results/s5/nidar')
a = ap.parse_args()
out = os.path.join(a.out_root, a.run_id); os.makedirs(out, exist_ok=True)
spec = importlib.util.spec_from_file_location('adapter', HERE + '/adapter.py'); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
tr = run_replay.load_truth(D + '/truth.csv')
lat0, lon0 = tr['lat'][0], tr['lon'][0]
N = (tr['lat'] - lat0) * 111320.0; E = (tr['lon'] - lon0) * 111320.0 * np.cos(np.radians(lat0))
vn = np.gradient(N, tr['t']); ve = np.gradient(E, tr['t']); vu = np.gradient(tr['alt'], tr['t'])
rng = np.random.default_rng(1000 + a.seed)
vn_n = vn + rng.normal(0, a.sig_v, len(vn)); ve_n = ve + rng.normal(0, a.sig_v, len(vn)); vu_n = vu + rng.normal(0, 0.3, len(vn))
t60 = {k: np.array([float(x[k]) for x in csv.DictReader(open(D + '/truth60.csv'))]) for k in ('t', 'roll', 'pitch')}
roll_n = np.degrees(np.interp(tr['t'], t60['t'], t60['roll'])) + rng.normal(0, 0.5, len(vn)); pitch_n = np.degrees(np.interp(tr['t'], t60['t'], t60['pitch'])) + rng.normal(0, 0.5, len(vn)); alt_n = tr['alt'] + rng.normal(0, 1.0, len(vn))
yaw_n = tr['yaw'] + rng.normal(0, a.sig_yaw, len(vn))
ad = m.Adapter(); t0 = time.time()
ad.init(D + '/map_esri_z19.png', dict(n=a.n, init_mode=a.init, res=a.res, seed=a.seed, label=a.label, rectify=a.rectify)); startup = time.time() - t0
files = sorted([f for f in os.listdir(D + '/frames') if f.endswith('.png')], key=lambda f: float(f[:-4]))
files = files[::a.stride]; files = [f for f in files if float(f[:-4]) >= a.t_start]
if a.max_frames: files = files[:a.max_frames]
est, lat_ms = [], []; log = open(out + '/log.txt', 'w')
dropped = 0; clock0 = time.time(); tfirst = float(files[0][:-4])
for f in files:
    t = float(f[:-4])
    if a.realtime:
        el = time.time() - clock0
        if el < t - tfirst: time.sleep(t - tfirst - el)
        elif el > (t - tfirst) + 0.2: dropped += 1; continue
    i = int(np.argmin(np.abs(tr['t'] - t)))
    img = cv2.imread(D + '/frames/' + f)
    if a.rectify: ad.set_attitude(roll_n[i], pitch_n[i], alt_n[i])
    if not a.no_odo: ad.set_odometry(vn_n[i], ve_n[i], vu_n[i], yaw_n[i])
    s = time.perf_counter(); r = ad.step(img, t, None); d = time.perf_counter() - s
    lat_ms.append(d * 1000); r['t'] = t
    r['raw_err'] = float(metrics.haversine(tr['lat'][i], tr['lon'][i], r['raw_lat'], r['raw_lon']))
    r['err'] = float(metrics.haversine(tr['lat'][i], tr['lon'][i], r['lat'], r['lon'])) if r['accepted'] else float('nan')
    est.append(r)
    msg = f"t={t:.1f} acc={r['accepted']} n={r['n_particles']} spread={r['spread_m']:.1f} raw_err={r['raw_err']:.1f} err={r['err']:.1f} step={d:.1f}s (seg {r['t_seg']:.1f} pf {r['t_pf']:.1f})"
    print(msg, flush=True); log.write(msg + '\n'); log.flush()
keys = ['t', 'lat', 'lon', 'alt', 'yaw', 'conf', 'accepted']
with open(out + '/estimates.csv', 'w', newline='') as fh:
    w = csv.DictWriter(fh, keys, extrasaction='ignore'); w.writeheader(); w.writerows(est)
ek = ['t', 'raw_lat', 'raw_lon', 'raw_err', 'err', 'n_particles', 'spread_m', 'converged', 'rms_m', 'frac', 't_seg', 't_pf']
with open(out + '/extras.csv', 'w', newline='') as fh:
    w = csv.DictWriter(fh, ek, extrasaction='ignore'); w.writeheader(); w.writerows(est)
E_ = {k: np.array([r[k] for r in est]) for k in ('t', 'lat', 'lon', 'accepted')}
mt = metrics.summarize(E_, tr, len(est))
errs = np.array([r['err'] for r in est]); acc = E_['accepted'].astype(bool)
good = acc & (errs <= 50); tconv = None
for k in range(len(est)):
    if acc[k] and np.all(errs[k:][acc[k:]] <= 50): tconv = float(E_['t'][k]) - float(E_['t'][0]); break
mt['time_converged_and_stays_50m'] = tconv
raw = np.array([r['raw_err'] for r in est]); mt['raw_centroid_median_last10'] = float(np.median(raw[-10:]))
mt['config'] = vars(a); mt['note'] = 'synthetic NIDAR-like; substitute semantic map from same segmenter; odometry=noisy telemetry velocity/yaw'
json.dump(mt, open(out + '/metrics.json', 'w'), indent=1)
json.dump(dict(startup_s=startup, dropped=dropped, frames_processed=len(est), latency_ms=lat_ms, peak_rss_mb=resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024,
               cpu='Intel Xeon 2.10GHz, 2 threads, nice', torch_threads=2, seed=a.seed, particles_initial=a.n), open(out + '/run.json', 'w'))
print(json.dumps(mt))
