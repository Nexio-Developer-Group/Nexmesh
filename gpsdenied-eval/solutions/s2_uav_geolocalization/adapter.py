"""S2 adapter: CAEVL retrieval + (OUR-VIO-SUBSTITUTE | OpenVINS trajectory file) + fuse.py causal fusion.
step() returns the FUSED estimate; extras: raw_lat/raw_lon (CAEVL top-1 fix, vehicle-position corrected for camera
tilt), vio_lat/vio_lon (VIO-only, anchored at the known start), flag, n_acc, score, ms_*.
Config keys (all optional): weights, tile_m, stride_m, imu_csv, start_latlon, corridor_m (None=off), consensus (bool),
vio ('substitute'), min_span, seed, threads."""
import os, sys, time, math
import numpy as np, cv2
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, HERE)
from s2lib import *
from ahrs import complementary_tilt
from vio_sub import VioSubstitute
import s2core

DEFAULT_START = (18.51770510, 73.85385750)       # take-off point (home), known before GNSS is lost
DEFAULT_WEIGHTS = os.path.join(HERE, 'weights/stagetwo.pth')


class Adapter:
    def init(self, map_path, config=None):
        c = dict(config or {})
        self.c = c
        img, self.ref, self.mpp = load_map()
        self.tile_m = float(c.get('tile_m', 120.0))
        self.R = s2core.Retriever(c.get('weights', DEFAULT_WEIGHTS), self.tile_m, float(c.get('stride_m', self.tile_m / 2)),
                                  threads=int(c.get('threads', 2)))
        self.corridor = c.get('corridor_m', None)
        self.imu_t, self.imu_att = complementary_tilt(c.get('imu_csv', os.path.join(ROOT, 'data/nidar/imu.csv')), 0.05)
        lat0, lon0 = c.get('start_latlon', DEFAULT_START)
        px, py = self.ref.latlon_to_px(lat0, lon0)
        self.start_en = np.array([px * self.mpp, -py * self.mpp])
        self.vio = VioSubstitute(yaw_bias_deg=c.get('vio_yaw_bias_deg', 0.0), scale_err=c.get('vio_scale_err', 0.0))
        self.fuser = s2core.StreamFuser(consensus=bool(c.get('consensus', True)), min_span=float(c.get('min_span', 300.0)),
                                        seed=int(c.get('seed', 0)))
        self.log = []                       # per-frame extras (list of dicts) for the driver
        self.last_alt = 200.0; self.last_yaw = 0.0

    def _latlon(self, en):
        la, lo = self.ref.px_to_latlon(en[0] / self.mpp, -en[1] / self.mpp); return float(la), float(lo)

    def step(self, frame, timestamp, hints):
        t = float(timestamp)
        g = frame if frame.ndim == 2 else cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY)
        alt = float(hints['baro_alt']) if hints and hints.get('baro_alt') else self.last_alt
        yaw = float(hints['att'][2]) if hints and hints.get('att') else self.last_yaw
        self.last_alt, self.last_yaw = alt, yaw
        k = min(int(np.searchsorted(self.imu_t, t)), len(self.imu_t) - 1)
        roll, pitch = self.imu_att[k]
        t0 = time.perf_counter()
        vio_rel, resp = self.vio.step(g, t, roll, pitch, yaw, alt)
        t_vio = (time.perf_counter() - t0) * 1000
        vio_en = self.start_en + vio_rel
        # retrieval
        t0 = time.perf_counter()
        crop, (n0, e0) = query_crop_att(g, roll, pitch, yaw, alt, self.tile_m)
        t_crop = (time.perf_counter() - t0) * 1000
        t0 = time.perf_counter(); d = self.R.encode(crop); t_enc = (time.perf_counter() - t0) * 1000
        prior = None
        if self.corridor:
            prior = (vio_en[0] + e0, vio_en[1] + n0)           # corridor centred on the motion prior (fused if available)
            if self.fuser.have is not None:
                sc, th, off = self.fuser.have; pe = sc * (fuse_rotz(th) @ vio_en) + off; prior = (pe[0] + e0, pe[1] + n0)
        i, score, ncand, t_search = self.R.search(d, prior, self.corridor)
        fix_en = self.R.cen[i] - np.array([e0, n0])
        # fusion
        t0 = time.perf_counter()
        est, info = self.fuser.step(t, vio_en, fix_en)
        t_fuse = (time.perf_counter() - t0) * 1000
        flag = 'fused'
        if est is None:
            est, flag = vio_en, 'warmup_vio_only'
        lat, lon = self._latlon(est); rl, ro = self._latlon(fix_en); vl, vo = self._latlon(vio_en)
        out = dict(t=t, lat=lat, lon=lon, alt=alt, yaw=math.degrees(yaw) % 360.0,
                   conf=float(min(1.0, info['n_acc'] / 50.0)) if flag == 'fused' else 0.1,
                   accepted=True, raw_lat=rl, raw_lon=ro, vio_lat=vl, vio_lon=vo, flag=flag,
                   n_acc=info['n_acc'], fix_arrival_ok=info['arrived_ok'], score=score, n_cand=ncand,
                   vio_resp=float(resp), roll=float(roll), pitch=float(pitch),
                   ms_vio=t_vio, ms_crop=t_crop, ms_encode=t_enc, ms_search=t_search, ms_fuse=t_fuse)
        self.log.append(out)
        return out


def fuse_rotz(a):
    return s2core.fuse.rotz(a)
