"""Diagnostic: where does a (north-up) NIDAR frame sit in the map, by template matching? Compares to truth-derived position."""
import sys, os, numpy as np, cv2
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, HERE + '/../../harness')
import geo, run_replay
D = HERE + '/../../data/nidar'; ref = geo.MapRef(D + '/map_esri_z19.json'); gm = ref.ground_m_per_px(18.52)
big = cv2.imread(D + '/map_esri_z19.png', 0); tr = run_replay.load_truth(D + '/truth.csv')
for t in [10, 20, 30, 40, 50, 60, 75, 90, 100, 115, 130, 145]:
    i = int(np.argmin(abs(tr['t'] - t))); X, Y = ref.latlon_to_px(tr['lat'][i], tr['lon'][i]); yaw = tr['yaw'][i]
    fr = cv2.imread(f'{D}/frames/{t:.3f}.png', 0)
    s = 0.18 / gm; f = cv2.resize(fr, None, fx=s, fy=s, interpolation=cv2.INTER_AREA)
    best = None
    for dy in (0,):
        # rotate frame to north-up (frame heading = yaw clockwise from north)
        h, w = f.shape; M = cv2.getRotationMatrix2D((w / 2, h / 2), yaw, 1.0)   # +angle = CCW; to undo CW heading rotate CCW by yaw? test both signs
        for sign in (1, -1):
            M = cv2.getRotationMatrix2D((w / 2, h / 2), sign * yaw, 1.0); c, sn = abs(M[0, 0]), abs(M[0, 1])
            nw, nh = int(h * sn + w * c), int(h * c + w * sn); M[0, 2] += nw / 2 - w / 2; M[1, 2] += nh / 2 - h / 2
            g = cv2.warpAffine(f, M, (nw, nh)); g = g[nh // 2 - 200:nh // 2 + 200, nw // 2 - 300:nw // 2 + 300]
            R = int(450 / gm); x0, y0 = int(X) - R, int(Y) - R
            win = big[y0:y0 + 2 * R, x0:x0 + 2 * R]
            r = cv2.matchTemplate(win, g, cv2.TM_CCOEFF_NORMED); _, mx, _, loc = cv2.minMaxLoc(r)
            cxm = x0 + loc[0] + g.shape[1] / 2; cym = y0 + loc[1] + g.shape[0] / 2
            if best is None or mx > best[0]: best = (mx, sign, (cym - Y) * gm, (cxm - X) * gm)
    mx_,my_=X+best[3]/gm, Y+best[2]/gm; lat,lon=ref.px_to_latlon(mx_,my_); d=np.hypot((tr['lat']-lat)*111320,(tr['lon']-lon)*111320*np.cos(np.radians(18.52))); j=int(np.argmin(d)); print('   nearest truth time to matched pos', tr['t'][j], 'dist', d[j])
    print(f't={t} yaw={yaw:.0f} best score {best[0]:.2f} sign {best[1]} offset south {best[2]:.1f} m east {best[3]:.1f} m', flush=True)
