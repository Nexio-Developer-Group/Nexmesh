"""S1 adapter: koksalan/gnss-denied-nav (gdnav.Localizer) behind the harness Adapter contract.

Pipeline actually executed (see ANALYSIS.md): north-up/scale warp (heading + baro height) -> DINOv2 global
descriptor -> top-k tiles (dot product on the tile DB) -> SuperPoint+LightGlue on precomputed map keypoints
-> RANSAC similarity -> planar PnP (position, height, tilt) -> consistency gate (baro height / IMU tilt).

config keys (all optional):
  weights        path to fine-tuned DINOv2 state dict (None = pretrained facebook/dinov2-base, DEGRADED)
  model          HF model id (default facebook/dinov2-base)
  patch_m        ground diameter compared (default 140 m = inscribed circle of our 1280x800 @0.18 m/px frame)
  tracking       bool, use the previous accepted fix as prior (repo 'track' mode) (default False = global search)
  gate           bool, consistency gate on (default True)
  use_baro       bool, use hints['baro_alt'] (else nominal_height) (default True)
  use_heading    bool, use hints['att'][2] (else heading 0) (default True)
  nominal_height height used when use_baro=False (default 200 m)
  yaw_unit       'deg' (what harness/run_replay.py really sends) or 'rad'
  rerank_k       top-k tiles for LightGlue (default 10)
  cache_dir      dir for cached tile DB / map keypoints (default solutions/s1.../cache)
"""
from __future__ import annotations

import json
import math
import os
import sys
import time
from pathlib import Path

import cv2
import numpy as np

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
sys.path.insert(0, str(ROOT / "harness"))
sys.path.insert(0, str(HERE / "repo"))
os.environ.setdefault("HF_HOME", str(HERE / "hf_cache"))
os.environ.setdefault("OMP_NUM_THREADS", "2")

import torch  # noqa: E402

torch.set_num_threads(2)
cv2.setNumThreads(2)

from geo import MapRef  # noqa: E402  (harness/geo.py)


class MapShim:
    """Duck-typed gdnav.geo.SatelliteMap over an in-memory north-up equirectangular RGB image.

    The harness map is Web-Mercator; gdnav expects linear lat/lon pixels, so it is resampled once with cv2.remap
    (at the map's native ground resolution) and kept in RAM (no GeoTIFF re-reads)."""

    def __init__(self, map_png: str, ref: MapRef):
        from gdnav.geo import meters_per_degree
        src = cv2.imread(map_png, cv2.IMREAD_COLOR)
        H, W = src.shape[:2]
        la_t, lo_l = ref.px_to_latlon(0, 0)
        la_b, lo_r = ref.px_to_latlon(W, H)
        la_c = (la_t + la_b) / 2
        self.m_lat, self.m_lon = meters_per_degree(float(la_c))
        mpp = ref.ground_m_per_px(la_c)
        self.dlat, self.dlon = -mpp / self.m_lat, mpp / self.m_lon
        self.lat0, self.lon0 = float(la_t), float(lo_l)
        self.height = int(round((la_b - la_t) / self.dlat))
        self.width = int(round((lo_r - lo_l) / self.dlon))
        jj, ii = np.meshgrid(np.arange(self.width), np.arange(self.height))
        px, py = ref.latlon_to_px(self.lat0 + (ii + 0.5) * self.dlat, self.lon0 + (jj + 0.5) * self.dlon)
        self.img = cv2.cvtColor(cv2.remap(src, px.astype(np.float32) - 0.5, py.astype(np.float32) - 0.5,
                                          cv2.INTER_LINEAR), cv2.COLOR_BGR2RGB)
        self.gsd_x = abs(self.dlon) * self.m_lon
        self.gsd_y = abs(self.dlat) * self.m_lat

    @property
    def bounds_latlon(self):
        return (self.lat0, self.lon0, self.lat0 + self.dlat * self.height, self.lon0 + self.dlon * self.width)

    @property
    def size_m(self):
        return self.width * self.gsd_x, self.height * self.gsd_y

    def latlon_to_px(self, lat, lon):
        return (np.asarray(lon) - self.lon0) / self.dlon, (np.asarray(lat) - self.lat0) / self.dlat

    def px_to_latlon(self, x, y):
        return self.lat0 + np.asarray(y) * self.dlat, self.lon0 + np.asarray(x) * self.dlon

    def crop(self, lat, lon, size_m, gsd):
        out = int(round(size_m / gsd))
        cx, cy = self.latlon_to_px(lat, lon)
        w, h = size_m / self.gsd_x, size_m / self.gsd_y
        x0, y0 = int(round(float(cx) - w / 2)), int(round(float(cy) - h / 2))
        wi, hi = int(round(w)), int(round(h))
        buf = np.zeros((hi, wi, 3), np.uint8)
        xa, ya, xb, yb = max(x0, 0), max(y0, 0), min(x0 + wi, self.width), min(y0 + hi, self.height)
        if xb > xa and yb > ya:
            buf[ya - y0:yb - y0, xa - x0:xb - x0] = self.img[ya:yb, xa:xb]
        return cv2.resize(buf, (out, out), interpolation=cv2.INTER_AREA)

    def read_overview(self, gsd):
        ow = int(round(self.width * self.gsd_x / gsd))
        oh = int(round(self.height * self.gsd_y / gsd))
        return cv2.resize(self.img, (ow, oh), interpolation=cv2.INTER_AREA)


def consistency_gate(fix, rel_alt_m, roll_deg, pitch_deg, degraded=False):
    """Verbatim logic of repo sim/visual_gps.py::consistency_gate (not importable: it pulls pymavlink/gazebo glue)."""
    min_inliers = 80 if degraded else 50
    if fix.inliers < min_inliers:
        return False, "few_inliers"
    if "height_m" not in fix.extra:
        return True, "no_pose"
    if abs(fix.extra["height_m"] - rel_alt_m) > 8.0:
        return False, "height"
    if not degraded and abs(fix.extra["off_nadir_deg"] - math.hypot(roll_deg, pitch_deg)) > 4.0:
        return False, "tilt"
    return True, "ok"


class Adapter:
    def init(self, map_path: str, config: dict) -> None:
        t0 = time.time()
        from gdnav import embed as embed_mod, localizer as loc_mod, mapfeatures as mf_mod
        from gdnav.localizer import Localizer, LocalizerConfig
        from gdnav.query import Camera
        self.cfg = dict(patch_m=140.0, tracking=False, gate=True, use_baro=True, use_heading=True,
                        nominal_height=200.0, yaw_unit="deg", rerank_k=10, model="facebook/dinov2-base",
                        weights=None, stride_m=50.0, cache_dir=str(HERE / "cache"), min_inliers=25)
        self.cfg.update(config or {})
        c = self.cfg
        cam = json.loads((HERE / "camera.json").read_text())
        self.cam = Camera(cam["focal_px"], cam["heading_sign"], cam["heading_offset_deg"])
        ref = MapRef(str(Path(map_path).with_suffix(".json")))
        self.sat = MapShim(map_path, ref)
        la0, lo0, la1, lo1 = self.sat.bounds_latlon
        bounds = (min(la0, la1), min(lo0, lo1), max(la0, la1), max(lo0, lo1))
        lcfg = LocalizerConfig(patch_m=c["patch_m"], stride_m=c["stride_m"], rerank_k=c["rerank_k"],
                               min_inliers=c["min_inliers"], model=c["model"], max_tilt_deg=25.0)
        os.makedirs(c["cache_dir"], exist_ok=True)
        tag = f"{Path(c['model']).name}_p{c['patch_m']:g}_s{c['stride_m']:g}"
        db_f, mf_f = Path(c["cache_dir"]) / f"db_{tag}.npy", Path(c["cache_dir"]) / f"mapf_{tag}.pt"
        info_f = Path(c["cache_dir"]) / f"build_{tag}.json"
        self.build_info = json.loads(info_f.read_text()) if info_f.exists() else {}
        # --- cache hooks: tile descriptors and map keypoints are expensive on CPU; build once, reuse ---
        orig_embed, orig_build = loc_mod.embed_images, mf_mod.MapFeatures.build
        state = {"pos": 0, "t_db": 0.0, "t_mf": 0.0}
        if db_f.exists():
            cached = np.load(db_f)

            def fake_embed(model, images, device, batch_size=128):
                out = cached[state["pos"]:state["pos"] + len(images)]
                state["pos"] += len(images)
                return out
            loc_mod.embed_images = fake_embed
        else:
            chunks = []

            def rec_embed(model, images, device, batch_size=128):
                t = time.time()
                e = orig_embed(model, images, device, batch_size)
                state["t_db"] += time.time() - t
                chunks.append(e)
                return e
            loc_mod.embed_images = rec_embed

        def build_cached(cls, *a, **kw):
            if mf_f.exists():
                d = torch.load(mf_f, weights_only=False)
                return cls(**d)
            t = time.time()
            m = orig_build(*a, **kw)
            state["t_mf"] = time.time() - t
            torch.save(dict(image=m.image, gsd=m.gsd, center_ll=m.center_ll, keypoints=m.keypoints,
                            descriptors=m.descriptors, scores=m.scores), mf_f)
            return m
        mf_mod.MapFeatures.build = classmethod(build_cached)
        try:
            self.loc = Localizer(self.sat, lcfg, c["weights"], device="cpu", bounds_ll=bounds)
        finally:
            loc_mod.embed_images, mf_mod.MapFeatures.build = orig_embed, orig_build
        if not db_f.exists():
            np.save(db_f, np.concatenate(chunks))
            self.build_info = dict(n_tiles=int(len(self.loc.tiles_ll)), db_dim=int(self.loc.db.shape[1]),
                                   db_bytes=int(self.loc.db.numel() * 4), descriptor_build_s=state["t_db"],
                                   mapfeat_build_s=state["t_mf"], n_map_keypoints=int(self.loc.mapf.keypoints.shape[0]),
                                   mapf_gsd=self.loc.mapf.gsd, mapf_image_px=list(self.loc.mapf.image.shape[:2]),
                                   mapf_bytes=int(self.loc.mapf.keypoints.numel() * 4 + self.loc.mapf.descriptors.numel() * 4
                                                  + self.loc.mapf.image.nbytes))
            info_f.write_text(json.dumps(self.build_info, indent=1))
        self.prior = None
        self.last_accept_t = -1e9
        self.n_frames = 0
        self.startup_s = time.time() - t0

    def step(self, frame, timestamp, hints):
        c = self.cfg
        self.n_frames += 1
        rgb = cv2.cvtColor(frame, cv2.COLOR_BGR2RGB) if frame.ndim == 3 else cv2.cvtColor(frame, cv2.COLOR_GRAY2RGB)
        height, heading, roll, pitch = c["nominal_height"], 0.0, 0.0, 0.0
        have_att = bool(hints) and hints.get("att") is not None
        if hints and c["use_baro"] and hints.get("baro_alt") is not None:
            height = float(hints["baro_alt"])
        if have_att:
            r, p, y = hints["att"]
            f = 1.0 if c["yaw_unit"] == "deg" else 180.0 / math.pi
            roll, pitch = float(r) * f, float(p) * f
            if c["use_heading"]:
                heading = float(y) * f
        t0 = time.perf_counter()
        prior = self.prior if c["tracking"] else None
        fix, mode = self.loc.localize(rgb, height, heading, self.cam, prior, roll_deg=roll, pitch_deg=pitch)
        dt = (time.perf_counter() - t0) * 1000
        out = dict(lat=float("nan"), lon=float("nan"), alt=None, yaw=heading if (have_att and c["use_heading"]) else None,
                   conf=0.0, accepted=False, mode=mode, gate="", ms=dt, n_inliers=int(fix.inliers),
                   raw_lat=float(fix.lat), raw_lon=float(fix.lon), height_m=fix.extra.get("height_m"),
                   off_nadir_deg=fix.extra.get("off_nadir_deg"), reproj_px=fix.extra.get("reproj_px"),
                   n_matches=fix.extra.get("n_matches"), rank=fix.rank, sim_top1=fix.extra.get("sim_top1"),
                   second_inliers=fix.extra.get("second_inliers"))
        if mode == "tilted":
            out["gate"] = "tilted"
            return out
        conf = fix.inliers >= self.loc.cfg.min_inliers
        gate = "off"
        if conf and c["gate"]:
            degraded = (timestamp - self.last_accept_t) > 5.0
            have_baro = bool(hints) and c["use_baro"] and hints.get("baro_alt") is not None
            if have_baro and "height_m" in fix.extra:
                conf, gate = consistency_gate(fix, height, roll if have_att else 0.0, pitch if have_att else 0.0, degraded)
            else:   # no independent height/tilt reference: only the inlier-count part of the gate applies
                conf, gate = (fix.inliers >= (80 if degraded else 50)), "inliers_only"
        elif not conf:
            gate = "few_inliers_raw"
        out["gate"] = gate
        if conf:
            self.prior, self.last_accept_t = (fix.lat, fix.lon), timestamp
            out.update(lat=float(fix.lat), lon=float(fix.lon), accepted=True,
                       alt=fix.extra.get("height_m"), conf=float(min(1.0, fix.inliers / 150.0)))
        elif gate in ("off", "few_inliers_raw"):
            self.prior = None   # genuinely lost: relocalize globally next frame (repo behaviour)
        return out
