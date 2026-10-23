"""Post-hoc analysis (no new filter runs): T6 scene classes, weather windows, failure figures, tables. Usage: python analyze.py <run-id>"""
import sys, os, json, csv, numpy as np, cv2
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, HERE); sys.path.insert(0, HERE + '/../../harness')
import adapter, run_replay, metrics
run = sys.argv[1]; R = HERE + '/../../results/s5'; D = HERE + '/../../data/nidar'
ex = list(csv.DictReader(open(f'{R}/nidar/{run}/extras.csv')))
t = np.array([float(r['t']) for r in ex]); raw = np.array([float(r['raw_err']) for r in ex]); conv = np.array([r['converged'] == 'True' for r in ex])
tr = run_replay.load_truth(D + '/truth.csv'); wx = json.load(open(D + '/weather.json'))
a = adapter.Adapter(); a.init(D + '/map_esri_z19.png', dict(n=1000, res=1.0)); lab = a.lab
a.pf.map_label  # label map loaded
def groups(i):
    X, Y = a.ref.latlon_to_px(tr['lat'][i], tr['lon'][i]); r0, c0 = (Y - a.Y0) * a.gm, (X - a.X0) * a.gm
    h = 50; w = lab[max(0, int(r0 - h)):int(r0 + h), max(0, int(c0 - h)):int(c0 + h)]
    fr = np.bincount(w.ravel(), minlength=7) / max(w.size, 1)
    b, v, wa, ro = fr[1] + fr[5], fr[3], fr[6], fr[2]
    return ('water' if wa > 0.10 else 'vegetation' if v > 0.40 else 'building' if b > 0.70 else 'mixed'), fr
G = []; FR = []
for tt in t:
    g, fr = groups(int(np.argmin(abs(tr['t'] - tt)))); G.append(g); FR.append(fr)
G = np.array(G); out = {}
for g in ('building', 'vegetation', 'water', 'mixed'):
    m = (G == g) & conv
    out[g] = dict(frames=int((G == g).sum()), converged_frames=int(m.sum()), median_err=float(np.median(raw[m])) if m.sum() else None, p90=float(np.percentile(raw[m], 90)) if m.sum() else None)
def inw(tt, ev): return any(e['t0'] <= tt <= e['t1'] for e in ev)
wm = np.array([inw(x, wx['blur']) for x in t]); hm = np.array([inw(x, wx['haze']) for x in t]); nm = ~wm & ~hm
for name, m in (('blur', wm), ('haze', hm), ('clear', nm)):
    mm = m & conv
    out['weather_' + name] = dict(frames=int(m.sum()), converged_frames=int(mm.sum()), median_err=float(np.median(raw[mm])) if mm.sum() else None, p90=float(np.percentile(raw[mm], 90)) if mm.sum() else None)
json.dump(out, open(f'{R}/nidar/{run}/analysis_T6_weather.json', 'w'), indent=1); print(json.dumps(out, indent=1))
# failure figures: 6 worst converged-or-not frames spaced >=8 s apart + weather examples
os.makedirs(f'{R}/figures/failures', exist_ok=True)
order = np.argsort(-np.nan_to_num(raw)); pick = []
for k in order:
    if all(abs(t[k] - t[p]) > 8 for p in pick): pick.append(k)
    if len(pick) == 6: break
pal = np.array([[60,60,60],[0,0,255],[200,200,200],[0,160,0],[255,0,255],[0,0,255],[255,0,0]], np.uint8)
big = cv2.imread(D + '/map_esri_z19.png'); notes = []
for k in pick:
    tt = t[k]; i = int(np.argmin(abs(tr['t'] - tt))); fr = cv2.imread(f'{D}/frames/{tt:.3f}.png')
    X, Y = a.ref.latlon_to_px(tr['lat'][i], tr['lon'][i]); ex_lat, ex_lon = float(ex[k]['raw_lat']), float(ex[k]['raw_lon']); EX, EY = a.ref.latlon_to_px(ex_lat, ex_lon)
    def crop(cx, cy): h = int(60 / a.gm); return cv2.resize(big[int(cy) - h:int(cy) + h, int(cx) - h:int(cx) + h], (300, 300))
    img = np.hstack([cv2.resize(fr, (480, 300)), crop(X, Y), crop(EX, EY)])
    cv2.imwrite(f'{R}/figures/failures/{run}_t{tt:.0f}_frame_truthcrop_estcrop.png', img)
    notes.append(dict(t=float(tt), raw_err_m=float(raw[k]), converged=bool(conv[k]), scene=str(G[k]), blur=bool(wm[k]), haze=bool(hm[k])))
json.dump(notes, open(f'{R}/figures/failures/{run}_failures.json', 'w'), indent=1); print(notes)
