import sys, os, numpy as np, cv2, torch
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__))); import adapter, run_replay
from PIL import Image
from feature_extraction.rrp import get_uav_rotate_g, rrp
D = adapter.HERE + '/../../data/nidar'; t = float(sys.argv[1])
a = adapter.Adapter(); a.init(D + '/map_esri_z19.png', dict(n=1000, res=1.0)); pf = a.pf
tr = run_replay.load_truth(D + '/truth.csv'); i = int(np.argmin(abs(tr['t'] - t)))
X, Y = a.ref.latlon_to_px(tr['lat'][i], tr['lon'][i]); cx, cy = (Y - a.Y0) * a.gm, (X - a.X0) * a.gm
fr = cv2.imread(f'{D}/frames/{t:.3f}.png'); _, pr = a.seg.detect_image(Image.fromarray(fr[:, :, ::-1])); pr = np.where(pr == 5, 1, pr)
yaw = tr['yaw'][i]; alt = tr['alt'][i]
pal = np.array([[60,60,60],[0,0,255],[200,200,200],[0,160,0],[255,0,255],[0,0,255],[255,0,0]], np.uint8)
R = rrp(torch.from_numpy(pr), -yaw, 400).squeeze(0).numpy() - 1      # uav label, north-up
cut = round(alt * pf.cam_view * pf.height_to_pix / 2)
c = pf.map_label_pad[int(cx) + pf.pad - cut:int(cx) + pf.pad + cut, int(cy) + pf.pad - cut:int(cy) + pf.pad + cut]
c = cv2.resize(c.astype(np.uint8), (400, 400), interpolation=cv2.INTER_NEAREST)
# true imagery crop from the map (north-up) same footprint
big = cv2.imread(D + '/map_esri_z19.png'); half = int(cut * a.res / a.gm)
img = big[int(Y) - half:int(Y) + half, int(X) - half:int(X) + half]; img = cv2.resize(img, (400, 400))
frot = cv2.cvtColor(cv2.resize(torch.from_numpy(fr[:, :, 0]).float().unsqueeze(0).numpy()[0].astype(np.uint8), (1280, 800)), cv2.COLOR_GRAY2BGR)
fr_n = rrp(torch.from_numpy(fr[:, :, 0].astype(np.int64)), -yaw, 400).squeeze(0).numpy() - 1
fr_n = cv2.cvtColor(fr_n.astype(np.uint8), cv2.COLOR_GRAY2BGR)
cv2.imwrite(f'work/viz_{t:.0f}.png', np.hstack([fr_n, pal[R], pal[c], img]))
print('cut', cut, 'yaw', yaw, 'alt', alt)
