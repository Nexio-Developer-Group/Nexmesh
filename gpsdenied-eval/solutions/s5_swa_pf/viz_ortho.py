import sys, os, numpy as np, cv2
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__))); import adapter, run_replay, csv
D = adapter.HERE + '/../../data/nidar'; t = float(sys.argv[1])
a = adapter.Adapter(); a.init(D + '/map_esri_z19.png', dict(n=1000, res=1.0, rectify=True))
tr = run_replay.load_truth(D + '/truth.csv'); i = int(np.argmin(abs(tr['t'] - t)))
t60 = list(csv.DictReader(open(D + '/truth60.csv'))); j = int(np.argmin([abs(float(r['t']) - t) for r in t60[::60]])) * 60
pitch = np.degrees(float(t60[j]['pitch'])); roll = np.degrees(float(t60[j]['roll']))
fr = cv2.imread(f'{D}/frames/{t:.3f}.png'); a.set_attitude(roll, pitch, tr['alt'][i]); o, G0 = a._ortho(fr, tr['yaw'][i]); print(pitch, G0)
# map crop around drone + G0 rotated heading-up for comparison
import geo
X, Y = a.ref.latlon_to_px(tr['lat'][i], tr['lon'][i]); big = cv2.imread(D + '/map_esri_z19.png')
cxp, cyp = X + G0[1] / a.gm, Y - G0[0] / a.gm   # centre ground point: north up in image => y decreases
half = int(120 / a.gm); crop = big[int(cyp) - half:int(cyp) + half, int(cxp) - half:int(cxp) + half]
M = cv2.getRotationMatrix2D((half, half), -tr['yaw'][i], 1.0)  # rotate map so heading is up
crop = cv2.warpAffine(crop, M, (2 * half, 2 * half)); crop = cv2.resize(crop, (480, 480))
cv2.imwrite('work/viz_ortho.png', np.hstack([cv2.resize(fr, (480, 300)), cv2.resize(o, (480, 300)), crop[90:390, :]]))
