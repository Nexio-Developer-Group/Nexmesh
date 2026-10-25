"""S5 engine: SWA-PF semantic-weighted adaptive particle filter (YuanJiayuuu/SWA-PF), wrapped around the repository's own
PFtool.ParticleState and SegFormer-B0 (the repository has no licence, so its code is never copied here: engines.s5.repo_path must point
to your own clone, `nexnav setup --s5`). Our adapter code (ported from the evaluated adapter) adds: CPU/GPU selection, a vectorised
motion update, an integral-image weight formula equivalent to the repo's (checked to 1.3e-3 relative on the evaluation machine),
a semantic label map built from the reference map with the repo's segmenter (OUR SUBSTITUTE for the paper's U-Net labelling),
altitude pinned to the telemetry altitude, and DBSCAN-based acceptance."""
import hashlib, io, contextlib, json, math, os, sys, time
import numpy as np
import cv2
from .base import Engine, Fix


def _chdir(path):
    class _C:
        def __enter__(s): s.o = os.getcwd(); os.chdir(path)
        def __exit__(s, *a): os.chdir(s.o)
    return _C()


class S5Engine(Engine):
    name = "s5"

    # ------------------------------------------------------------------ load
    def load(self, ctx):
        import torch
        cfg = ctx.cfg; s = self.c = cfg["engines"]["s5"]; self.log = ctx.log; self.ctx = ctx
        n = cfg["engines"]["threads"]
        if n: torch.set_num_threads(n)
        self.repo = os.path.abspath(os.path.expanduser(s["repo_path"]))
        if not os.path.isfile(os.path.join(self.repo, "PFtool.py")): raise FileNotFoundError(f"SWA-PF repository not found at {self.repo} (run `nexnav setup --s5`)")
        sys.path.insert(0, self.repo)
        with _chdir(self.repo), contextlib.redirect_stdout(io.StringIO()):
            import PFtool
            from segformer import SegFormer_Segmentation
            self.PFtool = PFtool
            self.seg = SegFormer_Segmentation(cuda=(ctx.device == "cuda"))
        self.gm = ctx.geomap; self.native_mpp = self.gm.ground_m_per_px(); self.res = float(s["label_res_m"])
        np.random.seed(cfg["run"]["seed"]); torch.manual_seed(cfg["run"]["seed"])
        self.lab, self.X0, self.Y0 = self._label_map()
        os.makedirs(os.path.join(ctx.cache_dir, "s5tmp"), exist_ok=True)
        lp, ip = os.path.join(ctx.cache_dir, "s5tmp", f"label_{os.getpid()}.png"), os.path.join(ctx.cache_dir, "s5tmp", f"img_{os.getpid()}.png")
        cv2.imwrite(lp, self.lab); cv2.imwrite(ip, np.zeros((4, 4, 3), np.uint8))
        H, W = self.lab.shape
        detail = [0.0, W * self.res, 0.0, H * self.res, 1.0 / self.res, 0, 0, 0]   # map "lat/lon" are local metres; height_to_pix = px per metre
        self.hmin, self.hmax = float(s["hmin_m"]), float(s["hmax_m"])
        State = self._state_class()
        t0 = time.time()
        with _chdir(self.repo), contextlib.redirect_stdout(io.StringIO()):
            self.pf = State([ip, lp], detail, [int(s["n_particles"]), self.hmax, self.hmin])
        os.remove(lp); os.remove(ip)
        i = ctx.intr
        cam_view = (int(min(i.width, i.height) / math.sqrt(2)) / i.fx) if s["cam_view"] == "auto" else float(s["cam_view"])
        self.pf.cam_view = cam_view; self.pf.res = self.res
        self.pf.init_mode(int(s["init_mode"]), np.random.default_rng(cfg["run"]["seed"]), dict(xy=s["sig_xy_m"], z=s["altitude_sigma_m"], yaw=s["sig_yaw_deg"]))
        self.prev = None; self.spread0 = None; self.k = 0; self.rng = np.random.default_rng(cfg["run"]["seed"] + 7)
        self.init_info = dict(label_shape=list(self.lab.shape), label_res_m=self.res, particles=int(s["n_particles"]), cam_view=cam_view, pf_init_s=time.time() - t0, device=ctx.device)

    def _label_map(self):
        """Semantic label map (classes 0 ground,1 building,2 road,3 vegetation,4 vehicle,5 roof,6 water) at label_res_m, built with the
        repo's SegFormer over footprint-sized windows of the reference map; cached. Returns (labels, X0, Y0) with X0/Y0 the native-map
        pixel of the label map's top-left corner."""
        s = self.c; res = self.res; intr = self.ctx.intr
        if s["label_map"]:
            lab = np.load(os.path.expanduser(s["label_map"])); meta = json.load(open(os.path.expanduser(s["label_map"]) + ".json")); return lab, meta["x0_px"], meta["y0_px"]
        gm = self.gm; roi = self.ctx.cfg["map"]["roi"]
        if roi:
            xs, ys = gm.latlon_to_px([roi[0], roi[2]], [roi[1], roi[3]]); x0, x1 = int(max(0, min(xs))), int(min(gm.width, max(xs))); y0, y1 = int(max(0, min(ys))), int(min(gm.height, max(ys)))
        else: x0, y0, x1, y1 = 0, 0, gm.width, gm.height
        key = hashlib.sha1(gm.gray[::32, ::32].tobytes() + f"{gm.width}x{gm.height}{x0}{y0}{x1}{y1}{res}{s['label_gsd_m']}".encode()).hexdigest()[:12]
        path = os.path.join(self.ctx.cache_dir, f"s5_labels_{key}.npy")
        if os.path.exists(path): return np.load(path), x0, y0
        from PIL import Image
        FW, FH = intr.width * s["label_gsd_m"], intr.height * s["label_gsd_m"]          # window footprint (m)
        wpx, hpx = int(round(FW / self.native_mpp)), int(round(FH / self.native_mpp))
        ow, oh = int(round(FW / res)), int(round(FH / res))
        nx, ny = int(math.ceil((x1 - x0) / wpx)), int(math.ceil((y1 - y0) / hpx))
        mosaic = np.zeros((ny * oh, nx * ow), np.uint8); t0 = time.time()
        for j in range(ny):
            for i in range(nx):
                x, y = x0 + i * wpx, y0 + j * hpx
                win = gm.bgr[y:y + hpx, x:x + wpx]
                if win.size == 0: continue
                win = cv2.resize(win, (intr.width, intr.height), interpolation=cv2.INTER_CUBIC)
                g = cv2.cvtColor(cv2.cvtColor(win, cv2.COLOR_BGR2GRAY), cv2.COLOR_GRAY2BGR)
                with _chdir(self.repo), contextlib.redirect_stdout(io.StringIO()):
                    _, pr = self.seg.detect_image(Image.fromarray(g[:, :, ::-1]))
                mosaic[j * oh:(j + 1) * oh, i * ow:(i + 1) * ow] = cv2.resize(pr.astype(np.uint8), (ow, oh), interpolation=cv2.INTER_NEAREST)
            self.log.info("S5 label map row %d/%d (%.0fs)", j + 1, ny, time.time() - t0)
        np.save(path, mosaic)
        return mosaic, x0, y0

    def _state_class(self):
        PFtool = self.PFtool

        class State(PFtool.ParticleState):
            def init_mode(self, mode, rng, sigmas):
                n = self.num_particles; self.sig = sigmas; p = np.zeros((n, 6), np.float32); r = rng.random((n, 4))
                if mode == 1:
                    f = 4; s_ = n // f; r[s_:2 * s_] = r[:s_]; r[2 * s_:3 * s_] = r[:s_]; r[3 * s_:] = r[:n - 3 * s_]
                    for m in range(f):
                        a = slice(m * s_, (m + 1) * s_ if m < f - 1 else n); r[a, 2] = m / (f - 1)
                elif mode == 2:
                    bi = np.argwhere(self.map_tags['b'] == 1)
                    if len(bi) == 0: bi = np.argwhere(self.map_tags['m'] >= 0)
                    k = bi[rng.integers(0, len(bi), n)]
                    r[:, 0] = (k[:, 0] + rng.random(n)) / self.map_height; r[:, 1] = (k[:, 1] + rng.random(n)) / self.map_width
                p[:, 0] = r[:, 0] * self.map_height; p[:, 1] = r[:, 1] * self.map_width
                p[:, 2] = r[:, 2] * (self.height_max - self.height_min) + self.height_min; p[:, 3] = r[:, 3] * 360
                self.particles = p; self.rng = rng

            def update_particles_fast(self, uav_img):
                from feature_extraction.rrp import get_uav_rotate_g
                R = self.resize; rot = get_uav_rotate_g(uav_img, self.device, R); wan = self.wan
                I = np.zeros((100, 4, R + 1, R + 1))
                for k in range(100):
                    u = rot[k].cpu().numpy().astype(np.float64) * wan[None]; I[k, :, 1:, 1:] = u.cumsum(1).cumsum(2)
                P = self.particles
                if not hasattr(self, '_bcache'): self._bcache = {}
                dist = [self.mapp_dist[k] for k in ('o', 'b', 'r', 'v')]
                for i in range(P.shape[0]):
                    x, y = P[i, 0], P[i, 1]
                    if x <= 0 or y <= 0 or x >= self.map_height or y >= self.map_width: P[i, 4] = 0; continue
                    tx, ty = round(float(x)), round(float(y))
                    cut = round(float(P[i, 2]) * self.cam_view * self.height_to_pix / 2); s_ = 2 * cut
                    if s_ < 2: P[i, 4] = 0; continue
                    x0, y0 = tx + self.pad - cut, ty + self.pad - cut
                    B = self._bcache.get(s_)
                    if B is None:
                        m = np.floor(np.arange(R) * np.float32(s_ / R)).astype(int); B = np.searchsorted(m, np.arange(s_ + 1)); B[s_] = R; self._bcache[s_] = B
                    Ik = I[int((360 - P[i, 3] + 1.8) / 3.6) % 100]
                    W = Ik[:, B[1:]][:, :, B[1:]] - Ik[:, B[:-1]][:, :, B[1:]] - Ik[:, B[1:]][:, :, B[:-1]] + Ik[:, B[:-1]][:, :, B[:-1]]
                    ws = 0.0
                    for c in range(4): ws += float((dist[c][x0:x0 + s_, y0:y0 + s_] * W[c]).sum())
                    ws += 10 + 100000 * float(self.dist_p[x0:x0 + s_, y0:y0 + s_].sum()); P[i, 4] = R * R / (ws * ws)
                self.remap()

            def move_np(self, dN, dE, dz, yaw_now, yaw_old):
                res = self.res; dlat_px, dlon_px = dN / res, dE / res
                theta = (90 if dlon_px > 0 else (270 if dlon_px < 0 else 0)) if dlat_px == 0 else math.degrees(math.atan(dlon_px / dlat_px)) + (0 if dlat_px > 0 else 180)
                length = math.hypot(dlat_px, dlon_px); th1 = theta - yaw_old; th2 = yaw_now - theta
                P = self.particles; n = len(P); g = self.rng.normal; a = np.radians(th1 + P[:, 3]); sx, sz, sy = self.sig['xy'] / res, self.sig['z'], self.sig['yaw']
                P[:, 0] = P[:, 0] - length * np.cos(a) + g(0, sx, n); P[:, 1] = P[:, 1] + length * np.sin(a) + g(0, sx, n)
                if self.height_min != self.height_max: P[:, 2] = np.clip(P[:, 2] + dz + g(0, sz, n), self.height_min, self.height_max)
                P[:, 3] = (P[:, 3] + th1 + th2 + g(0, sy, n)) % 360
                out = (P[:, 0] <= 0) | (P[:, 1] <= 0) | (P[:, 0] >= self.map_height) | (P[:, 1] >= self.map_width); P[out, :] = 0
        return State

    # ------------------------------------------------------------------ per frame
    def _ortho(self, frame, hints, yaw_deg):
        """Body-fixed camera: ortho-rectify with IMU roll/pitch + altitude (EXTENSION, as evaluated on the oblique set)."""
        roll, pitch, alt = hints.roll, hints.pitch, hints.alt_agl; i = self.ctx.intr; F = i.fx; H, W = frame.shape[:2]
        cr, sr, cp, sp, cy, sy = math.cos(roll), math.sin(roll), math.cos(pitch), math.sin(pitch), math.cos(math.radians(yaw_deg)), math.sin(math.radians(yaw_deg))
        Rz = np.array([[cy, -sy, 0], [sy, cy, 0], [0, 0, 1]]); Ry = np.array([[cp, 0, sp], [0, 1, 0], [-sp, 0, cp]]); Rx = np.array([[1, 0, 0], [0, cr, -sr], [0, sr, cr]])
        R = Rz @ Ry @ Rx; d0 = R @ np.array([0, 0, 1.0]); G0 = alt * d0[:2] / d0[2]; s = alt / F
        u, v = np.meshgrid(np.arange(W), np.arange(H)); f = (H / 2 - v) * s; r = (u - W / 2) * s
        Nn = G0[0] + f * cy - r * sy; Ee = G0[1] + f * sy + r * cy
        dc = np.einsum('ij,jhw->ihw', R.T, np.stack([Nn, Ee, np.full_like(Nn, alt)]))
        mu = W / 2 + F * dc[1] / dc[2]; mv = H / 2 - F * dc[0] / dc[2]
        return cv2.remap(frame, mu.astype(np.float32), mv.astype(np.float32), cv2.INTER_LINEAR, borderMode=cv2.BORDER_CONSTANT, borderValue=0), G0

    def _ll(self, x, y, g):
        """Particle (row x, col y; metres/res) -> lat/lon; g = (north, east) ground offset of the image centre from the drone."""
        return self.gm.px_to_latlon(self.X0 + (y * self.res - g[1]) / self.native_mpp, self.Y0 + (x * self.res + g[0]) / self.native_mpp)

    def process(self, frame, hints):
        from PIL import Image
        from sklearn.cluster import DBSCAN
        t_start = time.perf_counter(); s = self.c; pf = self.pf; t = frame.t
        yaw_deg = (math.degrees(hints.yaw) + self.ctx.cfg["camera"]["mount_yaw_deg"]) % 360
        if self.prev is not None and s["odometry"] == "telemetry":
            dt = t - self.prev[0]
            vn, ve, vu = hints.vn, hints.ve, hints.vu
            if s["odometry_sigma_mps"]: vn, ve = vn + self.rng.normal(0, s["odometry_sigma_mps"]), ve + self.rng.normal(0, s["odometry_sigma_mps"])
            pf.move_np(vn * dt, ve * dt, vu * dt, yaw_deg, self.prev[1])
        self.prev = (t, yaw_deg)
        if s["altitude_from_telemetry"]:
            pf.particles[:, 2] = np.clip(hints.alt_agl + self.rng.normal(0, s["altitude_sigma_m"], len(pf.particles)), self.hmin, self.hmax)
        img = frame.image; G0 = np.zeros(2)
        if self.ctx.cfg["camera"]["mount"] == "body_fixed": img, G0 = self._ortho(img, hints, yaw_deg)
        t1 = time.perf_counter()
        with _chdir(self.repo), contextlib.redirect_stdout(io.StringIO()):
            _, pr = self.seg.detect_image(Image.fromarray(img[:, :, ::-1]))
        pr = np.where(pr == 5, 1, pr)
        t2 = time.perf_counter()
        with _chdir(self.repo), contextlib.redirect_stdout(io.StringIO()):
            pf.update_particles_fast(pr)
        t3 = time.perf_counter()
        P = pf.particles[:, :4]; ok = (P[:, 0] > 0) & (P[:, 1] > 0); Pk = P[ok] if ok.sum() > 50 else P
        cen = Pk[:, :2].mean(0); spread = float(np.sqrt(((Pk[:, :2] - cen) ** 2).sum(1)).mean() * self.res)
        if self.spread0 is None and spread > 0: self.spread0 = spread
        converged = spread < s["conv_ratio"] * (self.spread0 or 1e9)
        fix = Fix(t, self.name, False, info=dict(n_particles=int(pf.num_particles), spread_m=spread, converged=bool(converged), t_seg=t2 - t1, t_pf=t3 - t2))
        ce = cen
        if converged:
            lab = DBSCAN(eps=s["eps_m"] / self.res, min_samples=s["min_samples"]).fit_predict(Pk[:, :2])
            if (lab >= 0).any():
                big = np.bincount(lab[lab >= 0]).argmax(); m = lab == big; ce = Pk[m, :2].mean(0); frac = float(m.mean())
                rms = float(np.sqrt(((Pk[m, :2] - ce) ** 2).sum(1).mean()) * self.res); lat, lon = self._ll(ce[0], ce[1], G0)
                th = np.radians(Pk[m, 3])
                fix.lat, fix.lon = float(lat), float(lon); fix.alt = float(Pk[m, 2].mean()); fix.yaw = math.degrees(math.atan2(np.sin(th).mean(), np.cos(th).mean())) % 360
                fix.conf = float(np.clip(1 - rms / 50, 0, 1) * frac); fix.accepted = bool(frac >= s["accept_frac"]); fix.info.update(frac=frac, rms_m=rms)
        rl, rn = self._ll(ce[0], ce[1], G0); fix.raw_lat, fix.raw_lon = float(rl), float(rn)
        fix.latency_s = time.perf_counter() - t_start; self.k += 1
        return fix
