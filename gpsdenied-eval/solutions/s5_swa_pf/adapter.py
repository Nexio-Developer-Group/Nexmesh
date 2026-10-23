"""Adapter for S5 = SWA-PF (YuanJiayuuu/SWA-PF), CPU, using the repo's own PFtool.ParticleState (unmodified) via subclass.

OUR SUBSTITUTE SEMANTIC MAP: label map = repo drone SegFormer-B0 run on data/nidar/map_esri_z19.png windows (build_semmap.py).
Frame labels = same segmenter on the frame. NOT the paper's U-Net satellite labelling; agreement is optimistic.

Deviations from the repo (all adapter-side; repo/ untouched):
 * SegFormer_Segmentation(cuda=False)  (CPU)
 * map "lat/lon" are local metres (north, east) so the repo's lat/lon-delta move() maths works in metres
 * cam_view calibrated to our camera (square crop of the frame = 565 px * 0.18 m = 101.7 m -> cam_view 0.5085 x altitude)
 * move(): same formulas, vectorised, noise sigmas configurable, mean-0 (repo: gauss(2,15) px, gauss(2,8) m)
 * one resample per frame (repo demo resamples twice: inside update_particles and again in SWAPF.py)
 * estimate = DBSCAN cluster centre on (x,y) (repo's center() clusters all 6 columns, plots, and prints GT error)
Odometry is NOT in harness hints: driver calls adapter.set_odometry(v_north, v_east, v_up, yaw_deg) before step().
"""
import os, sys, math, time, json
import numpy as np, cv2, torch
HERE = os.path.dirname(os.path.abspath(__file__))
REPO = HERE + '/repo'
sys.path.insert(0, REPO); sys.path.insert(0, HERE + '/../../harness')
torch.set_num_threads(2)
import geo
from PIL import Image
from sklearn.cluster import DBSCAN
_cwd = os.getcwd(); os.chdir(REPO)
import PFtool
from segformer import SegFormer_Segmentation
os.chdir(_cwd)
_SEG = None
def get_seg():
    global _SEG
    if _SEG is None:
        c = os.getcwd(); os.chdir(REPO)
        import io, contextlib
        with contextlib.redirect_stdout(io.StringIO()):
            _SEG = SegFormer_Segmentation(cuda=False)
        os.chdir(c)
    return _SEG

class State(PFtool.ParticleState):
    def init_mode(self, mode, rng, sigmas):
        n = self.num_particles
        self.sig = sigmas
        p = np.zeros((n, 6), np.float32)
        r = rng.random((n, 4))
        if mode == 0:     # full random (repo init_method_mode 0)
            pass
        elif mode == 1:   # layered random (4 altitude floors, same xy/yaw)
            f = 4; s = n // f
            r[s:2 * s] = r[:s]; r[2 * s:3 * s] = r[:s]; r[3 * s:] = r[:n - 3 * s]
            for m in range(f):
                a = slice(m * s, (m + 1) * s if m < f - 1 else n)
                r[a, 2] = m / (f - 1)
        elif mode == 2:   # semantic alignment: xy sampled on building pixels (repo mode 2)
            bi = np.argwhere(self.map_tags['b'] == 1)
            if len(bi) == 0: bi = np.argwhere(self.map_tags['m'] >= 0)
            k = bi[rng.integers(0, len(bi), n)]
            r[:, 0] = (k[:, 0] + rng.random(n)) / self.map_height; r[:, 1] = (k[:, 1] + rng.random(n)) / self.map_width
        p[:, 0] = r[:, 0] * self.map_height; p[:, 1] = r[:, 1] * self.map_width
        p[:, 2] = r[:, 2] * (self.height_max - self.height_min) + self.height_min
        p[:, 3] = r[:, 3] * 360
        self.particles = p
        self.rng = rng


    def update_particles_fast(self, uav_img):
        """Mathematically equivalent to PFtool.ParticleState.update_particles (same weight formula), but the nearest-neighbour
        resize of each map crop to 400x400 is folded into an integral image of (drone one-hot x bowl-weight), so each particle
        costs O(crop^2) instead of O(400^2 x 4). Verified against the repo implementation (see verify_fast.py)."""
        from feature_extraction.rrp import get_uav_rotate_g
        R = self.resize
        rot = get_uav_rotate_g(uav_img, self.device, R)
        wan = self.wan
        I = np.zeros((100, 4, R + 1, R + 1))
        for k in range(100):
            u = rot[k].cpu().numpy().astype(np.float64) * wan[None]
            I[k, :, 1:, 1:] = u.cumsum(1).cumsum(2)
        P = self.particles
        if not hasattr(self, '_bcache'): self._bcache = {}
        dist = [self.mapp_dist[k] for k in ('o', 'b', 'r', 'v')]
        for i in range(P.shape[0]):
            x, y = P[i, 0], P[i, 1]
            if x <= 0 or y <= 0 or x >= self.map_height or y >= self.map_width:
                P[i, 4] = 0; continue
            tx, ty = round(float(x)), round(float(y))
            cut = round(float(P[i, 2]) * self.cam_view * self.height_to_pix / 2); s = 2 * cut
            x0, y0 = tx + self.pad - cut, ty + self.pad - cut
            B = self._bcache.get(s)
            if B is None:
                m = np.floor(np.arange(R) * np.float32(s / R)).astype(int)
                B = np.searchsorted(m, np.arange(s + 1)); B[s] = R; self._bcache[s] = B
            Ik = I[int((360 - P[i, 3] + 1.8) / 3.6) % 100]
            W = Ik[:, B[1:]][:, :, B[1:]] - Ik[:, B[:-1]][:, :, B[1:]] - Ik[:, B[1:]][:, :, B[:-1]] + Ik[:, B[:-1]][:, :, B[:-1]]
            ws = 0.0
            for c in range(4): ws += float((dist[c][x0:x0 + s, y0:y0 + s] * W[c]).sum())
            ws += 10 + 100000 * float(self.dist_p[x0:x0 + s, y0:y0 + s].sum())
            P[i, 4] = R * R / (ws * ws)
        self.remap()

    def move_np(self, dN, dE, dz, yaw_now, yaw_old):
        """Same formulas as PFtool.ParticleState.move but vectorised; dN,dE,dz in metres (map 'lat/lon' are metres)."""
        res = self.res
        dlat_px, dlon_px = dN / res, dE / res
        if dlat_px == 0:
            theta = 90 if dlon_px > 0 else (270 if dlon_px < 0 else 0)
        else:
            theta = math.degrees(math.atan(dlon_px / dlat_px)) + (0 if dlat_px > 0 else 180)
        length = math.hypot(dlat_px, dlon_px)
        th1 = theta - yaw_old; th2 = yaw_now - theta
        P = self.particles; n = len(P); g = self.rng.normal
        a = np.radians(th1 + P[:, 3])
        sx, sz, sy = self.sig['xy'] / res, self.sig['z'], self.sig['yaw']
        P[:, 0] = P[:, 0] - length * np.cos(a) + g(0, sx, n)
        P[:, 1] = P[:, 1] + length * np.sin(a) + g(0, sx, n)
        if self.height_min != self.height_max:
            P[:, 2] = np.clip(P[:, 2] + dz + g(0, sz, n), self.height_min, self.height_max)
        P[:, 3] = (P[:, 3] + th1 + th2 + g(0, sy, n)) % 360
        out = (P[:, 0] <= 0) | (P[:, 1] <= 0) | (P[:, 0] >= self.map_height) | (P[:, 1] >= self.map_width)
        P[out, :] = 0

class Adapter:
    def init(self, map_path, config=None):
        c = dict(res=1.0, n=5000, n_min=1000, hmin=150., hmax=250., init_mode=0, seed=0, sig_xy=8., sig_z=8., sig_yaw=0.2,
                 label=None, cam_view=0.5085, eps_m=15., min_samples=20, conv_ratio=0.10, frac=0.5, use_baro=False)
        c.update(config or {}); self.cfg = c
        random_seed = c['seed']; np.random.seed(random_seed); torch.manual_seed(random_seed)
        res = c['res']
        meta = json.load(open(HERE + '/work/semmap_meta.json'))
        lab = np.load(c['label'] or f"{HERE}/work/semmap_{res}.npy")
        self.res = res; self.lab = lab
        self.ref = geo.MapRef(os.path.splitext(map_path)[0] + '.json'); self.gm = meta['gm']; self.X0, self.Y0 = meta['x0_px'], meta['y0_px']
        os.makedirs(HERE + '/work/tmp', exist_ok=True)
        lp, ip = f"{HERE}/work/tmp/label_{os.getpid()}.png", f"{HERE}/work/tmp/img_{os.getpid()}.png"
        cv2.imwrite(lp, lab); cv2.imwrite(ip, np.zeros((4, 4, 3), np.uint8))
        H, W = lab.shape
        detail = [0.0, W * res, 0.0, H * res, 1.0 / res, 0, 0, 0]   # lon_min/max, lat_min/max = local metres, height_to_pix = px per m
        cwd = os.getcwd(); os.chdir(REPO)
        import io, contextlib
        with contextlib.redirect_stdout(io.StringIO()):
            self.pf = State([ip, lp], detail, [c['n'], c['hmax'], c['hmin']])
        os.chdir(cwd)
        os.remove(lp); os.remove(ip)
        self.pf.cam_view = c['cam_view']; self.pf.res = res
        self.pf.init_mode(c['init_mode'], np.random.default_rng(random_seed), dict(xy=c['sig_xy'], z=c['sig_z'], yaw=c['sig_yaw']))
        self.seg = get_seg(); self.odo = None; self.prev = None; self.spread0 = None; self.k = 0
        self.extra = {}
        self.map_cls = np.zeros(7)

    def set_attitude(self, roll_deg, pitch_deg, alt_m):
        """EXTENSION (not in the repo): IMU roll/pitch + baro height, used to ortho-rectify the oblique frame (camera is body-fixed and
        pitches ~-30 deg at cruise, so the image centre is ~115 m behind the drone). Enabled by config rectify=True."""
        self.att = (roll_deg, pitch_deg, alt_m)

    def _ortho(self, frame, yaw_deg):
        roll, pitch, alt = np.radians(self.att[0]), np.radians(self.att[1]), self.att[2]
        F = 1108.5125; H, W = frame.shape[:2]
        cr, sr, cp, sp, cy, sy = math.cos(roll), math.sin(roll), math.cos(pitch), math.sin(pitch), math.cos(math.radians(yaw_deg)), math.sin(math.radians(yaw_deg))
        Rz = np.array([[cy, -sy, 0], [sy, cy, 0], [0, 0, 1]]); Ry = np.array([[cp, 0, sp], [0, 1, 0], [-sp, 0, cp]]); Rx = np.array([[1, 0, 0], [0, cr, -sr], [0, sr, cr]])
        R = Rz @ Ry @ Rx
        d0 = R @ np.array([0, 0, 1.0]); G0 = alt * d0[:2] / d0[2]            # ground point under image centre (north, east) rel. to drone
        s = alt / F
        u, v = np.meshgrid(np.arange(W), np.arange(H)); f = (H / 2 - v) * s; r = (u - W / 2) * s
        Nn = G0[0] + f * cy - r * sy; Ee = G0[1] + f * sy + r * cy
        dc = np.einsum('ij,jhw->ihw', R.T, np.stack([Nn, Ee, np.full_like(Nn, alt)]))
        mu = W / 2 + F * dc[1] / dc[2]; mv = H / 2 - F * dc[0] / dc[2]
        out = cv2.remap(frame, mu.astype(np.float32), mv.astype(np.float32), cv2.INTER_LINEAR, borderMode=cv2.BORDER_CONSTANT, borderValue=0)
        return out, G0

    def set_odometry(self, v_north, v_east, v_up, yaw_deg):
        self.odo = (v_north, v_east, v_up, yaw_deg)

    def _ll(self, x, y):
        g = getattr(self, 'G0', np.zeros(2))   # drone = image-centre ground point - G0 (north, east metres); x is row (south+), y is col (east+)
        return self.ref.px_to_latlon(self.X0 + (y * self.res - g[1]) / self.gm, self.Y0 + (x * self.res + g[0]) / self.gm)

    def step(self, frame, timestamp, hints=None):
        t0 = time.time(); pf = self.pf
        if self.prev is not None and self.odo is not None:
            dt = timestamp - self.prev[0]; vn, ve, vu, yaw = self.odo
            pf.move_np(vn * dt, ve * dt, vu * dt, yaw, self.prev[1])
        if self.odo is not None: self.prev = (timestamp, self.odo[3])
        if frame.ndim == 2: frame = np.dstack([frame] * 3)
        self.G0 = np.zeros(2)
        if self.cfg.get('rectify') and getattr(self, 'att', None) is not None and self.odo is not None:
            frame, self.G0 = self._ortho(frame, self.odo[3])
        t1 = time.time()
        _, pr = self.seg.detect_image(Image.fromarray(frame[:, :, ::-1]))
        pr = np.where(pr == 5, 1, pr)                       # roof -> building, as SWAPF.py
        t2 = time.time()
        n_before = pf.num_particles
        c = os.getcwd(); os.chdir(REPO)
        (pf.update_particles_fast if self.cfg.get('fast', True) else pf.update_particles)(pr)  # weights + one resample (80%, floor 1000)
        os.chdir(c)
        if pf.num_particles < self.cfg['n_min']: pass
        t3 = time.time()
        P = pf.particles[:, :4]
        ok = (P[:, 0] > 0) & (P[:, 1] > 0)
        Pk = P[ok] if ok.sum() > 50 else P
        cen = Pk[:, :2].mean(0)
        spread = float(np.sqrt(((Pk[:, :2] - cen) ** 2).sum(1)).mean() * self.res)
        if self.spread0 is None and spread > 0: self.spread0 = spread
        converged = spread < self.cfg['conv_ratio'] * (self.spread0 or 1e9)
        out = dict(lat=0.0, lon=0.0, alt=None, yaw=None, conf=0.0, accepted=False, n_particles=int(pf.num_particles),
                   spread_m=spread, converged=bool(converged), t_seg=t2 - t1, t_pf=t3 - t2, t_move=t1 - t0, n_before=int(n_before))
        # candidate estimate always reported as raw_*, accepted only when converged
        ce = cen; frac = 1.0
        if converged:
            lab = DBSCAN(eps=self.cfg['eps_m'] / self.res, min_samples=self.cfg['min_samples']).fit_predict(Pk[:, :2])
            if (lab >= 0).any():
                big = np.bincount(lab[lab >= 0]).argmax(); m = lab == big
                ce = Pk[m, :2].mean(0); frac = m.mean()
                rms = float(np.sqrt(((Pk[m, :2] - ce) ** 2).sum(1).mean()) * self.res)
                lat, lon = self._ll(ce[0], ce[1])
                th = np.radians(Pk[m, 3]); yaw = math.degrees(math.atan2(np.sin(th).mean(), np.cos(th).mean())) % 360
                out.update(lat=float(lat), lon=float(lon), alt=float(Pk[m, 2].mean()), yaw=yaw, frac=float(frac), rms_m=rms,
                           conf=float(np.clip(1 - rms / 50, 0, 1) * frac), accepted=bool(frac >= self.cfg['frac']))
        rl, rn = self._ll(ce[0], ce[1]); out.update(raw_lat=float(rl), raw_lon=float(rn))
        self.k += 1
        return out
