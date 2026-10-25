"""S6 engine ("our composite"): MegaLoc retrieval -> XFeat + LighterGlue matching -> RANSAC homography -> planar PnP ->
consistency gate -> tracking window. Ported from the evaluated implementation (solutions/s6_jetson_composite/s6lib.py) with these changes:
camera intrinsics come from the config, the map comes from any GeoMap, retrieval uses a numpy dot product (no FAISS), the descriptor
index has several tile scales (so one install covers a 100-500 m altitude range), canvas / crop / reference-patch sizes follow the
image footprint, and tensors run on CUDA when available (the CUDA path was NOT tested in the evaluation: no GPU there)."""
import hashlib, json, math, os, sys, time
import numpy as np
import cv2
from .base import Engine, Fix


def _third_party(cfg, sub):
    return os.path.join(os.path.expanduser(cfg["engines"]["third_party_dir"]), sub)


# ---------------------------------------------------------------- retrievers / matchers
class MegaLocRet:
    name = "megaloc"

    def __init__(self, cfg, device):
        import torch
        self.torch, self.dev, self.fp16 = torch, device, bool(cfg["engines"]["s6"]["fp16"]) and device == "cuda"
        d = _third_party(cfg, "MegaLoc")
        if not os.path.isdir(d): raise FileNotFoundError(f"MegaLoc not found at {d}: run `nexnav setup --s6`")
        self.m = torch.hub.load(d, "get_trained_model", source="local").eval().to(device)
        self.mean = torch.tensor([0.485, 0.456, 0.406], device=device).view(1, 3, 1, 1); self.std = torch.tensor([0.229, 0.224, 0.225], device=device).view(1, 3, 1, 1)

    def embed(self, grays):
        torch = self.torch; out = []
        with torch.inference_mode():
            for g in grays:
                x = torch.from_numpy(g).to(self.dev).float().div(255)[None, None].repeat(1, 3, 1, 1)
                with torch.autocast("cuda", dtype=torch.float16, enabled=self.fp16):
                    out.append(self.m((x - self.mean) / self.std)[0].float().cpu().numpy())
        return np.stack(out).astype(np.float32)


class DinoRet:
    name = "dinov2s"

    def __init__(self, cfg, device):
        import torch, timm
        self.torch, self.dev = torch, device
        self.m = timm.create_model("vit_small_patch14_dinov2.lvd142m", pretrained=True, num_classes=0, dynamic_img_size=True).eval().to(device)
        self.mean = torch.tensor([0.485, 0.456, 0.406], device=device).view(1, 3, 1, 1); self.std = torch.tensor([0.229, 0.224, 0.225], device=device).view(1, 3, 1, 1)

    def embed(self, grays):
        torch = self.torch; out = []
        with torch.inference_mode():
            for g in grays:
                h, w = (g.shape[0] // 14) * 14, (g.shape[1] // 14) * 14
                g = cv2.resize(g, (w, h), interpolation=cv2.INTER_AREA)
                x = torch.from_numpy(g).to(self.dev).float().div(255)[None, None].repeat(1, 3, 1, 1)
                f = self.m.forward_features((x - self.mean) / self.std)[0]
                cls, pt = f[0], f[1:]
                d = torch.cat([torch.nn.functional.normalize(cls, dim=0), torch.nn.functional.normalize(pt.mean(0), dim=0)])
                out.append(torch.nn.functional.normalize(d, dim=0).cpu().numpy())
        return np.stack(out).astype(np.float32)


class XFeatMatcher:
    def __init__(self, cfg, device, use_lg=True):
        import torch
        self.torch = torch
        d = _third_party(cfg, "accelerated_features")
        if not os.path.isdir(d): raise FileNotFoundError(f"XFeat not found at {d}: run `nexnav setup --s6`")
        sys.path.insert(0, d)
        from modules.xfeat import XFeat
        self.kpts = cfg["engines"]["s6"]["kpts"]; self.x = XFeat(top_k=self.kpts); self.use_lg = use_lg; self.name = "xfeat_lg" if use_lg else "xfeat_mnn"

    def extract(self, gray):
        torch = self.torch; h, w = gray.shape
        with torch.inference_mode():
            t = torch.from_numpy(gray).float()[None, None] / 255.0
            d = self.x.detectAndCompute(t, top_k=self.kpts)[0]
        d["image_size"] = (w, h)
        return d

    def match(self, d0, d1):
        torch = self.torch
        with torch.inference_mode():
            if self.use_lg:
                if len(d0["keypoints"]) < 8 or len(d1["keypoints"]) < 8: return np.zeros((0, 2)), np.zeros((0, 2))
                a, b, _ = self.x.match_lighterglue(d0, d1, min_conf=0.1)
                return a, b
            i0, i1 = self.x.match(d0["descriptors"], d1["descriptors"], min_cossim=0.82)
            return d0["keypoints"][i0].cpu().numpy(), d1["keypoints"][i1].cpu().numpy()


class SPLGMatcher:
    name = "splg"

    def __init__(self, cfg, device):
        import torch
        from lightglue import LightGlue, SuperPoint
        self.torch, self.dev = torch, device
        self.sp = SuperPoint(max_num_keypoints=min(cfg["engines"]["s6"]["kpts"], 2048)).eval().to(device); self.lg = LightGlue(features="superpoint").eval().to(device)

    def extract(self, gray):
        torch = self.torch
        with torch.inference_mode():
            f = self.sp.extract(torch.from_numpy(gray).float()[None, None].to(self.dev) / 255.0)
        f["image_size"] = (gray.shape[1], gray.shape[0]); return f

    def match(self, d0, d1):
        torch = self.torch
        with torch.inference_mode():
            m = self.lg({"image0": d0, "image1": d1})["matches"][0]
        if len(m) == 0: return np.zeros((0, 2)), np.zeros((0, 2))
        return d0["keypoints"][0][m[:, 0]].cpu().numpy(), d1["keypoints"][0][m[:, 1]].cpu().numpy()


# ---------------------------------------------------------------- geometry
def body_rot(roll, pitch, yaw):
    cr, sr, cp, sp, cy, sy = math.cos(roll), math.sin(roll), math.cos(pitch), math.sin(pitch), math.cos(yaw), math.sin(yaw)
    Rz = np.array([[cy, -sy, 0], [sy, cy, 0], [0, 0, 1]]); Ry = np.array([[cp, 0, sp], [0, 1, 0], [-sp, 0, cp]]); Rx = np.array([[1, 0, 0], [0, cr, -sr], [0, sr, cr]])
    return Rz @ Ry @ Rx


class Geom:
    """Camera geometry for one intrinsics set."""
    def __init__(self, intr):
        self.i = intr; self.K = intr.K

    def ground_pts(self, uv, R, h):
        """Frame px (N,2) -> ground (east, north) metres relative to the point below the camera. Body: x forward = image up, y right, z down."""
        i = self.i; uv = np.asarray(uv, float)
        d = (R @ np.stack([(i.cy - uv[:, 1]) / i.fy, (uv[:, 0] - i.cx) / i.fx, np.ones(len(uv))])).T
        k = h / d[:, 2]
        return np.stack([d[:, 1] * k, d[:, 0] * k], 1)

    def corners(self):
        i = self.i; return np.float32([[0, 0], [i.width, 0], [i.width, i.height], [0, i.height]])

    def footprint_extent(self, R, h):
        en = self.ground_pts(self.corners(), R, h); return en.max(0) - en.min(0)   # (east extent, north extent) m

    def warp_query(self, gray, R, h, mpp, size):
        """Rectify the frame onto a north-up canvas at map resolution. Returns img, mask, G (frame px -> canvas px), c (east,north metres of the canvas centre rel. to nadir)."""
        corners = self.corners(); en = self.ground_pts(corners, R, h)
        c = (en.min(0) + en.max(0)) / 2
        dst = np.float32([[size / 2 + (e - c[0]) / mpp, size / 2 - (n - c[1]) / mpp] for e, n in en])
        G = cv2.getPerspectiveTransform(corners, dst)
        src = cv2.GaussianBlur(gray, (0, 0), 0.8)
        img = cv2.warpPerspective(src, G, (size, size), flags=cv2.INTER_LINEAR, borderValue=0)
        mask = cv2.warpPerspective(np.full(gray.shape, 255, np.uint8), G, (size, size), flags=cv2.INTER_NEAREST, borderValue=0)
        return img, mask, G, c


def apply_h(G, pts):
    p = np.hstack([pts, np.ones((len(pts), 1))]) @ G.T
    return p[:, :2] / p[:, 2:3]


def pnp_pose(obj_xy_m, img_px, thr_m, K):
    """Planar PnP (IPPE) on homography-RANSAC inliers. obj in map metres (x = east, y = south, z = 0)."""
    if len(obj_xy_m) < 6: return None
    H, inl = cv2.findHomography(img_px.astype(np.float32), obj_xy_m.astype(np.float32), cv2.USAC_MAGSAC, thr_m)
    if H is None or inl is None: return None
    inl = inl.ravel().astype(bool)
    if inl.sum() < 6: return None
    o3 = np.hstack([obj_xy_m[inl], np.zeros((inl.sum(), 1))]).astype(np.float64); ip = img_px[inl].astype(np.float64)
    ok, rvec, tvec = cv2.solvePnP(o3, ip, K, None, flags=cv2.SOLVEPNP_IPPE)
    if not ok: return None
    rvec, tvec = cv2.solvePnPRefineLM(o3, ip, K, None, rvec, tvec)
    Rm, _ = cv2.Rodrigues(rvec); C = (-Rm.T @ tvec).ravel()
    proj, _ = cv2.projectPoints(o3, rvec, tvec, K, None); res = np.linalg.norm(proj.reshape(-1, 2) - ip, axis=1)
    axis = Rm.T @ np.array([0, 0, 1.0]); tilt = math.degrees(math.acos(np.clip(axis[2], -1, 1)))
    right = Rm.T @ np.array([1.0, 0, 0]); yaw = math.atan2(right[1], right[0])
    return dict(C=C, height=-C[2], tilt=tilt, yaw=yaw, n_inl=int(inl.sum()), res_med=float(np.median(res)))


class TileIndex:
    """Descriptors of map tiles at one scale; exact inner-product search with numpy (a few thousand tiles: no FAISS needed)."""
    def __init__(self, gray, mpp, tile_m, cfg, ret, cache_dir, key, log):
        s = cfg["engines"]["s6"]
        self.T = max(64, int(round(tile_m / mpp))); self.S = max(16, int(round(self.T * s["stride_frac"]))); self.tile_m = tile_m
        H, W = gray.shape
        self.origins = [(x, y) for y in range(0, max(H - self.T, 0) + 1, self.S) for x in range(0, max(W - self.T, 0) + 1, self.S)]
        self.centers = np.array([(x + self.T / 2, y + self.T / 2) for x, y in self.origins], np.float32)
        path = os.path.join(cache_dir, f"s6_{ret.name}_{key}_T{self.T}_S{self.S}_r{s['ret_scale']}.npy")
        self.build_s = 0.0
        if os.path.exists(path): D = np.load(path)
        else:
            t0 = time.time(); ds = []
            for i, (x, y) in enumerate(self.origins):
                t = cv2.resize(gray[y:y + self.T, x:x + self.T], None, fx=s["ret_scale"], fy=s["ret_scale"], interpolation=cv2.INTER_AREA)
                ds.append(ret.embed([t])[0])
                if i % 100 == 0: log.info("S6 index %dm: %d/%d tiles (%.0fs)", int(tile_m), i, len(self.origins), time.time() - t0)
            D = np.stack(ds); self.build_s = time.time() - t0; np.save(path, D)
        self.D = D; self.size_mb = D.nbytes / 1e6

    def search(self, q, k):
        s = self.D @ q.astype(np.float32)
        k = min(k, len(s)); i = np.argpartition(-s, k - 1)[:k]; i = i[np.argsort(-s[i])]
        return i, s[i]


class S6Engine(Engine):
    name = "s6"

    def load(self, ctx):
        import torch
        cfg = ctx.cfg; s = self.c = cfg["engines"]["s6"]; self.log = ctx.log; self.ctx = ctx
        n = cfg["engines"]["threads"]
        if n: torch.set_num_threads(n); cv2.setNumThreads(n)
        for k, env in (("hf_home", "HF_HOME"), ("torch_home", "TORCH_HOME")):
            if cfg["engines"][k]: os.environ[env] = os.path.expanduser(cfg["engines"][k])
        self.dev = ctx.device
        gm = ctx.geomap; native = gm.ground_m_per_px(); work = cfg["map"]["work_gsd_m"] or native
        self.wmap = gm.resampled(native / work); self.map = self.wmap.gray; self.mpp = work
        self.geom = Geom(ctx.intr); self.K = ctx.intr.K
        t0 = time.time()
        self.ret = {"megaloc": MegaLocRet, "dinov2s": DinoRet}[s["retriever"]](cfg, self.dev)
        self.matcher = {"xfeat_lg": lambda: XFeatMatcher(cfg, self.dev, True), "xfeat_mnn": lambda: XFeatMatcher(cfg, self.dev, False), "splg": lambda: SPLGMatcher(cfg, self.dev)}[s["matcher"]]()
        self.log.info("S6 models loaded in %.1fs on %s", time.time() - t0, self.dev)
        os.makedirs(ctx.cache_dir, exist_ok=True)
        key = hashlib.sha1(self.map[::32, ::32].tobytes() + str(self.map.shape).encode() + f"{self.mpp:.4f}".encode()).hexdigest()[:12]
        self.indices = []
        t0 = time.time()
        for tm in sorted(s["tile_scales_m"]): self.indices.append(TileIndex(self.map, self.mpp, tm, cfg, self.ret, ctx.cache_dir, key, self.log))
        self.init_info = dict(index_s=time.time() - t0, index_build_s=sum(i.build_s for i in self.indices), index_mb=sum(i.size_mb for i in self.indices),
                              tiles={f"{i.tile_m:.0f}m": len(i.origins) for i in self.indices}, work_gsd_m=self.mpp, device=self.dev)
        self.ref_cache = {}; self.last = None; self.vel = np.zeros(2); self.miss = 0; self.prev_t = None

    # ---- helpers
    def _ref_px(self, extent_m):
        c = self.c; return int(min(self.map.shape[0], self.map.shape[1], round(max(c["ref_size_m"], 1.6 * max(extent_m)) / self.mpp), c.get("ref_max_px", 2560)))

    def _canvas(self, extent_m):
        c = self.c
        if c["nu_size"] != "auto": return int(c["nu_size"])
        return int(min(c["max_canvas_px"], max(1024, math.ceil(1.1 * math.hypot(*extent_m) / self.mpp / 64) * 64)))

    def _patch(self, cx, cy, S):
        H, W = self.map.shape
        return int(np.clip(round(cx - S / 2), 0, W - S)), int(np.clip(round(cy - S / 2), 0, H - S))

    def _ref_feats(self, ox, oy, S, cache=True):
        key = (ox, oy, S)
        if cache and key in self.ref_cache: return self.ref_cache[key]
        f = self.matcher.extract(self.map[oy:oy + S, ox:ox + S])
        if cache:
            if len(self.ref_cache) > self.c["ref_cache"]: self.ref_cache.pop(next(iter(self.ref_cache)))
            self.ref_cache[key] = f
        return f

    def _query_feats(self, img, mask):
        import torch
        f = self.matcher.extract(img)
        er = cv2.erode(mask, np.ones((15, 15), np.uint8))
        kp = f["keypoints"]; kp = (kp[0] if kp.dim() == 3 else kp).cpu().numpy()
        xi = np.clip(kp[:, 0].astype(int), 0, mask.shape[1] - 1); yi = np.clip(kp[:, 1].astype(int), 0, mask.shape[0] - 1)
        keep = er[yi, xi] > 0
        return self._filter(f, keep)

    @staticmethod
    def _filter(f, keep):
        import torch
        if "scores" in f and f["keypoints"].dim() == 2:
            m = torch.from_numpy(keep).to(f["keypoints"].device)
            return {k: (v[m] if torch.is_tensor(v) and v.dim() >= 1 and v.shape[0] == len(keep) else v) for k, v in f.items()}
        out = dict(f)
        for k in ("keypoints", "keypoint_scores", "descriptors"):
            if k in f: out[k] = f[k][:, torch.from_numpy(keep).to(f[k].device)]
        return out

    def _solve(self, fq, G, ox, oy, fr):
        c = self.c; t0 = time.perf_counter()
        pq, pr = self.matcher.match(fq, fr); tm = time.perf_counter() - t0
        out = dict(n_match=len(pq), t_match=tm, ok=False)
        if len(pq) < 8: return out
        img_px = apply_h(np.linalg.inv(G), np.asarray(pq, np.float64)); obj = (np.asarray(pr, np.float64) + np.array([ox, oy])) * self.mpp
        pose = pnp_pose(obj, img_px, c["ransac_px"] * self.mpp, self.K)
        if pose is None: return out
        px = pose["C"][:2] / self.mpp
        lat, lon = self.wmap.px_to_latlon(px[0], px[1])
        out.update(pose=pose, n_inl=pose["n_inl"], ok=True, px=px, lat=float(lat), lon=float(lon))
        return out

    def _gate(self, cand, baro, att, att_reliable, prev_pred, dt):
        c = self.c; p = cand["pose"]; fl = []
        if p["n_inl"] < c["min_inl"]: fl.append("few_inliers")
        if p["res_med"] > c["max_res_px"]: fl.append("high_resid")
        if c["gate"]:
            if abs(p["height"] - baro) > max(c["gate_h_min_m"], c["gate_h_frac"] * baro): fl.append("height")
            if p["tilt"] > 50: fl.append("tilt_range")
            if att is not None and att_reliable:
                ti = math.degrees(math.acos(np.clip(math.cos(att[0]) * math.cos(att[1]), -1, 1)))
                if abs(p["tilt"] - ti) > c["gate_tilt_deg"]: fl.append("tilt_imu")
            if att is not None:
                dy = abs((math.degrees(p["yaw"] - att[2]) + 180) % 360 - 180)
                if dy > c["gate_yaw_deg"]: fl.append("yaw_compass")
            if prev_pred is not None and np.linalg.norm(cand["px"] - prev_pred) * self.mpp > c["track_vmax_mps"] * dt + c["track_margin_m"]: fl.append("jump")
        return fl

    # ---- main entry
    def process(self, frame, hints):
        t_start = time.perf_counter(); c = self.c
        gray = cv2.cvtColor(frame.image, cv2.COLOR_BGR2GRAY); t = frame.t
        roll, pitch, yaw = hints.roll, hints.pitch, hints.yaw
        att_reliable = self.ctx.cfg["camera"]["mount"] == "body_fixed"
        if not att_reliable: roll = pitch = 0.0
        yaw = (yaw + math.radians(self.ctx.cfg["camera"]["mount_yaw_deg"])) % (2 * math.pi)
        baro = hints.alt_agl; att = (roll, pitch, yaw)
        R = body_rot(roll, pitch, yaw); ext = self.geom.footprint_extent(R, baro); size = self._canvas(ext)
        img, mask, G, cen = self.geom.warp_query(gray, R, baro, self.mpp, size)
        fq = self._query_feats(img, mask)
        res = dict(mode="none", flags="", n_inl=0, cand=0, canvas=size)
        dt = (t - self.prev_t) if self.prev_t is not None else 0.2
        S = self._ref_px(ext)
        if c["tracking"] and self.last is not None and self.miss < c["track_miss_reset"]:
            pred = self.last + self.vel * dt
            ox, oy = self._patch(*(pred + np.array([cen[0] / self.mpp, -cen[1] / self.mpp])), S)
            cand = self._solve(fq, G, ox, oy, self._ref_feats(ox, oy, S, cache=False)); res["mode"] = "track"; res["cand"] = 1
            if cand["ok"]:
                fl = self._gate(cand, baro, att, att_reliable, pred, dt)
                if cand["pose"]["n_inl"] < c["track_min_inl"] and "few_inliers" not in fl: fl.append("few_inliers")
                res.update(n_inl=cand["n_inl"], flags=",".join(fl))
                if not fl: return self._accept(res, cand, t, dt, t_start)
            self.miss += 1; res["flags"] += ";track_fail"
        # global: pick the tile scale whose size matches the query footprint, retrieve, verify
        q_m = c["q_crop_frac"] * (self.geom.i.height / self.geom.i.fy) * baro   # central crop side (m): fraction of the footprint's short side
        index = min(self.indices, key=lambda ix: abs(math.log(ix.tile_m / max(q_m * 1.33, 1.0))))
        qpx = int(min(size, max(64, round(q_m / self.mpp)))); m = size // 2
        crop = cv2.resize(img[m - qpx // 2:m + qpx // 2, m - qpx // 2:m + qpx // 2], None, fx=c["ret_scale"], fy=c["ret_scale"], interpolation=cv2.INTER_AREA)
        q = self.ret.embed([crop])[0]; idx, sc = index.search(q, c["topk"])
        best = None; ncand = 0
        for i in idx:
            ox, oy = self._patch(*index.centers[i], S)
            cand = self._solve(fq, G, ox, oy, self._ref_feats(ox, oy, S)); ncand += 1
            if not cand["ok"]: continue
            fl = self._gate(cand, baro, att, att_reliable, None, dt); cand["flags"] = fl; cand["tile"] = int(i)
            if best is None or (not fl and (best["flags"] or cand["n_inl"] > best["n_inl"])) or (fl and best["flags"] and cand["n_inl"] > best["n_inl"]): best = cand
            if not fl and cand["n_inl"] >= c["early_inl"]: break
        res["mode"] = "track+global" if res["mode"] == "track" else "global"; res["cand"] += ncand; res["tile_scale_m"] = index.tile_m
        raw = (float("nan"), float("nan"))
        if best is not None:
            res.update(n_inl=best["n_inl"], flags=(res["flags"] + "|" if res["flags"] else "") + ",".join(best["flags"]))
            raw = (best["lat"], best["lon"])
            if not best["flags"]: return self._accept(res, best, t, dt, t_start)
        else: res["flags"] = (res["flags"] + "|" if res["flags"] else "") + "no_pose"
        if self.last is not None: self.miss += 1
        return Fix(t, self.name, False, raw_lat=raw[0], raw_lon=raw[1], latency_s=time.perf_counter() - t_start, info=res)

    def _accept(self, res, cand, t, dt, t_start):
        px = cand["px"]
        if self.last is not None and dt > 0:
            v = (px - self.last) / dt; self.vel = 0.5 * self.vel + 0.5 * v if self.miss == 0 else v / 2
        self.last = px.copy(); self.prev_t = t; self.miss = 0
        p = cand["pose"]; res.update(res_med=p["res_med"], tilt=p["tilt"])
        return Fix(t, self.name, True, cand["lat"], cand["lon"], float(p["height"]), float(math.degrees(p["yaw"]) % 360), float(min(1.0, p["n_inl"] / 100.0)),
                   time.perf_counter() - t_start, cand["lat"], cand["lon"], res)
