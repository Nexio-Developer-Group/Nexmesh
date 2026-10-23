"""Our composite (S6): MegaLoc retrieval + XFeat/LighterGlue matching + planar PnP + consistency gate + tracking window.
Written from scratch for this evaluation; structure follows the retrieve-match-PnP idea of hloc, gate/tracking ideas of S1.
CPU only; threads limited to 2 (shared box)."""
import os, sys, time, json, math
import numpy as np
import cv2

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.join(HERE, "repo")
os.environ.setdefault("OMP_NUM_THREADS", "2")
os.environ.setdefault("HF_HOME", os.path.join(REPO, "hf"))
os.environ.setdefault("TORCH_HOME", os.path.join(REPO, "torchhome"))
import torch
torch.set_num_threads(int(os.environ.get("S6_THREADS", "2")))
cv2.setNumThreads(int(os.environ.get("S6_THREADS", "2")))
sys.path.insert(0, os.path.join(HERE, "..", "..", "harness"))
sys.path.insert(0, os.path.join(REPO, "accelerated_features"))
import geo  # noqa

CAM = json.load(open(os.path.join(HERE, "..", "..", "data", "nidar", "camera.json")))
K = np.array([[CAM["fx"], 0, CAM["cx"]], [0, CAM["fy"], CAM["cy"]], [0, 0, 1.0]])

DEFAULT = dict(
    retriever="megaloc",       # megaloc | dinov2s
    matcher="xfeat_lg",        # xfeat_lg | splg | xfeat_mnn
    tile=512, stride=256,      # map px (map res, 0.283 m/px)
    q_crop=384,                # centre crop of north-up query used for retrieval (map px)
    ret_scale=0.5,             # retrieval image scale (map px -> net px)
    topk=5, early_inl=70,
    nu_size=1024,              # rectified north-up query canvas (map px)
    ref_size=1280,             # ref patch for matching (map px, native map res)
    kpts=2048,
    min_inl=20, ransac_px=4.0,
    gate=True, gate_h_frac=0.10, gate_h_min=12.0, gate_tilt_deg=10.0, gate_yaw_deg=15.0, max_res_px=3.0, att_reliable=True,
    tracking=False, track_miss_reset=3, track_vmax=30.0, track_margin=40.0, track_min_inl=15,
    cache_dir=os.path.join(REPO, "cache"),
    default_alt=200.0,
)


# ------------------------------------------------------------------ retrievers
class MegaLocRet:
    name = "megaloc"
    def __init__(self):
        self.m = torch.hub.load(os.path.join(REPO, "MegaLoc"), "get_trained_model", source="local").eval()
        self.mean = torch.tensor([0.485, 0.456, 0.406]).view(1, 3, 1, 1); self.std = torch.tensor([0.229, 0.224, 0.225]).view(1, 3, 1, 1)
    @torch.inference_mode()
    def embed(self, grays):
        out = []
        for g in grays:
            x = torch.from_numpy(g).float().div(255)[None, None].repeat(1, 3, 1, 1)
            out.append(self.m((x - self.mean) / self.std)[0].numpy())
        return np.stack(out).astype(np.float32)


class DinoRet:
    name = "dinov2s"
    def __init__(self):
        import timm
        self.m = timm.create_model("vit_small_patch14_dinov2.lvd142m", pretrained=True, num_classes=0, dynamic_img_size=True).eval()
        self.mean = torch.tensor([0.485, 0.456, 0.406]).view(1, 3, 1, 1); self.std = torch.tensor([0.229, 0.224, 0.225]).view(1, 3, 1, 1)
    @torch.inference_mode()
    def embed(self, grays):
        out = []
        for g in grays:
            h, w = (g.shape[0] // 14) * 14, (g.shape[1] // 14) * 14
            g = cv2.resize(g, (w, h), interpolation=cv2.INTER_AREA)
            x = torch.from_numpy(g).float().div(255)[None, None].repeat(1, 3, 1, 1)
            f = self.m.forward_features((x - self.mean) / self.std)[0]
            cls, pt = f[0], f[1:]
            d = torch.cat([torch.nn.functional.normalize(cls, dim=0), torch.nn.functional.normalize(pt.mean(0), dim=0)])
            out.append(torch.nn.functional.normalize(d, dim=0).numpy())
        return np.stack(out).astype(np.float32)


def make_retriever(name):
    return {"megaloc": MegaLocRet, "dinov2s": DinoRet}[name]()


# ------------------------------------------------------------------ matchers
class XFeatLG:
    name = "xfeat_lg"
    def __init__(self, kpts=2048, use_lg=True):
        from modules.xfeat import XFeat
        self.x = XFeat(top_k=kpts); self.use_lg = use_lg
        if not use_lg: self.name = "xfeat_mnn"
    @torch.inference_mode()
    def extract(self, gray):
        h, w = gray.shape
        t = torch.from_numpy(gray).float()[None, None] / 255.0
        d = self.x.detectAndCompute(t, top_k=self.x.top_k)[0]
        d["image_size"] = (w, h)
        return d
    @torch.inference_mode()
    def match(self, d0, d1):
        if self.use_lg:
            if len(d0["keypoints"]) < 8 or len(d1["keypoints"]) < 8: return np.zeros((0, 2)), np.zeros((0, 2))
            a, b, _ = self.x.match_lighterglue(d0, d1, min_conf=0.1)
            return a, b
        idx0, idx1 = self.x.match(d0["descriptors"], d1["descriptors"], min_cossim=0.82)
        return d0["keypoints"][idx0].numpy(), d1["keypoints"][idx1].numpy()


class SPLG:
    name = "splg"
    def __init__(self, kpts=2048):
        from lightglue import LightGlue, SuperPoint
        self.sp = SuperPoint(max_num_keypoints=kpts).eval(); self.lg = LightGlue(features="superpoint").eval()
    @torch.inference_mode()
    def extract(self, gray):
        t = torch.from_numpy(gray).float()[None, None] / 255.0
        f = self.sp.extract(t)
        f["image_size"] = (gray.shape[1], gray.shape[0])
        return f
    @torch.inference_mode()
    def match(self, d0, d1):
        o = self.lg({"image0": d0, "image1": d1})
        m = o["matches"][0]
        if len(m) == 0: return np.zeros((0, 2)), np.zeros((0, 2))
        return d0["keypoints"][0][m[:, 0]].numpy(), d1["keypoints"][0][m[:, 1]].numpy()


def make_matcher(name, kpts):
    if name == "xfeat_lg": return XFeatLG(kpts, True)
    if name == "xfeat_mnn": return XFeatLG(kpts, False)
    if name == "splg": return SPLG(min(kpts, 2048))
    raise ValueError(name)


# ------------------------------------------------------------------ geometry
def body_rot(roll, pitch, yaw):
    cr, sr, cp, sp, cy, sy = math.cos(roll), math.sin(roll), math.cos(pitch), math.sin(pitch), math.cos(yaw), math.sin(yaw)
    Rz = np.array([[cy, -sy, 0], [sy, cy, 0], [0, 0, 1]]); Ry = np.array([[cp, 0, sp], [0, 1, 0], [-sp, 0, cp]])
    Rx = np.array([[1, 0, 0], [0, cr, -sr], [0, sr, cr]])
    return Rz @ Ry @ Rx


def ground_pts(uv, R, h):
    """Ray-cast frame px (N,2) to ground (east, north) metres relative to the point below the camera.
    Body convention (x fwd = image up, y right, z down), same as pinhole with fx=fy."""
    uv = np.asarray(uv, float)
    d = (R @ np.stack([(CAM["cy"] - uv[:, 1]) / CAM["fx"], (uv[:, 0] - CAM["cx"]) / CAM["fx"], np.ones(len(uv))])).T
    k = h / d[:, 2]
    return np.stack([d[:, 1] * k, d[:, 0] * k], 1)   # east, north


def warp_query(frame_gray, yaw, roll, pitch, h, map_mpp, size):
    """Rectify the raw frame onto a north-up canvas at map resolution (ground-plane homography from attitude + baro height).
    Returns img, mask, G (3x3 frame px -> canvas px), c (east,north metres of canvas centre rel. to nadir)."""
    W, H = CAM["width"], CAM["height"]
    R = body_rot(roll, pitch, yaw)
    corners = np.float32([[0, 0], [W, 0], [W, H], [0, H]])
    en = ground_pts(corners, R, h)
    # canvas centre = centre of the (clipped) footprint bounding box
    c = (en.min(0) + en.max(0)) / 2
    dst = np.float32([[size / 2 + (e - c[0]) / map_mpp, size / 2 - (n - c[1]) / map_mpp] for e, n in en])
    G = cv2.getPerspectiveTransform(corners, dst)
    src = cv2.GaussianBlur(frame_gray, (0, 0), 0.8)
    img = cv2.warpPerspective(src, G, (size, size), flags=cv2.INTER_LINEAR, borderValue=0)
    mask = cv2.warpPerspective(np.full(frame_gray.shape, 255, np.uint8), G, (size, size), flags=cv2.INTER_NEAREST, borderValue=0)
    return img, mask, G, c


def apply_h(G, pts):
    p = np.hstack([pts, np.ones((len(pts), 1))]) @ G.T
    return p[:, :2] / p[:, 2:3]


def pnp_pose(obj_xy_m, img_px, thr_m):
    """Planar PnP (IPPE) on homography-RANSAC inliers. obj in map metres (x=E, y=S, z=0). Returns dict or None."""
    n = len(obj_xy_m)
    if n < 6: return None
    H, inl = cv2.findHomography(img_px.astype(np.float32), obj_xy_m.astype(np.float32), cv2.USAC_MAGSAC, thr_m)
    if H is None or inl is None: return None
    inl = inl.ravel().astype(bool)
    if inl.sum() < 6: return None
    o3 = np.hstack([obj_xy_m[inl], np.zeros((inl.sum(), 1))]).astype(np.float64)
    ip = img_px[inl].astype(np.float64)
    ok, rvec, tvec = cv2.solvePnP(o3, ip, K, None, flags=cv2.SOLVEPNP_IPPE)
    if not ok: return None
    rvec, tvec = cv2.solvePnPRefineLM(o3, ip, K, None, rvec, tvec)
    Rm, _ = cv2.Rodrigues(rvec)
    C = (-Rm.T @ tvec).ravel()
    proj, _ = cv2.projectPoints(o3, rvec, tvec, K, None)
    res = np.linalg.norm(proj.reshape(-1, 2) - ip, axis=1)
    axis = Rm.T @ np.array([0, 0, 1.0])
    tilt = math.degrees(math.acos(np.clip(axis[2], -1, 1)))
    right = Rm.T @ np.array([1.0, 0, 0])
    yaw = math.atan2(right[1], right[0])
    return dict(C=C, height=-C[2], tilt=tilt, yaw=yaw, n_inl=int(inl.sum()), res_med=float(np.median(res)), inl=inl)


class TileIndex:
    def __init__(self, map_bgr_gray, cfg, retriever):
        import faiss
        self.cfg = cfg; self.ret = retriever
        T, S = cfg["tile"], cfg["stride"]
        n = map_bgr_gray.shape[0]
        self.origins = [(x, y) for y in range(0, n - T + 1, S) for x in range(0, n - T + 1, S)]
        self.centers = np.array([(x + T / 2, y + T / 2) for x, y in self.origins], np.float32)
        os.makedirs(cfg["cache_dir"], exist_ok=True)
        key = f"{retriever.name}_T{T}_S{S}_r{cfg['ret_scale']}_n{n}.npy"
        path = os.path.join(cfg["cache_dir"], key)
        self.build_s = 0.0
        if os.path.exists(path):
            D = np.load(path)
        else:
            t0 = time.time(); ds = []
            for i, (x, y) in enumerate(self.origins):
                t = cv2.resize(map_bgr_gray[y:y + T, x:x + T], None, fx=cfg["ret_scale"], fy=cfg["ret_scale"], interpolation=cv2.INTER_AREA)
                ds.append(retriever.embed([t])[0])
                if i % 50 == 0: print(f"index {i}/{len(self.origins)} {time.time()-t0:.0f}s", flush=True)
            D = np.stack(ds); self.build_s = time.time() - t0
            np.save(path, D)
            json.dump({"build_s": self.build_s, "n": len(D), "dim": int(D.shape[1])}, open(path + ".json", "w"))
        self.D = D
        self.index = faiss.IndexFlatIP(D.shape[1]); self.index.add(D)
        self.size_mb = D.nbytes / 1e6

    def search(self, q, k):
        s, i = self.index.search(q[None].astype(np.float32), k)
        return i[0], s[0]


class Pipeline:
    """retrieve (MegaLoc/FAISS) -> match (XFeat+LighterGlue) -> RANSAC -> planar PnP -> gate -> tracking window."""
    def __init__(self, map_path, cfg=None):
        self.cfg = dict(DEFAULT); self.cfg.update(cfg or {})
        c = self.cfg
        self.ref = geo.MapRef(os.path.splitext(map_path)[0] + ".json")
        self.map = cv2.imread(map_path, cv2.IMREAD_GRAYSCALE)
        self.mpp = self.ref.ground_m_per_px(self.ref.px_to_latlon(self.map.shape[1] / 2, self.map.shape[0] / 2)[0])
        self.t_init = {}
        t0 = time.time(); self.ret = make_retriever(c["retriever"]); self.t_init["retriever_load_s"] = time.time() - t0
        t0 = time.time(); self.matcher = make_matcher(c["matcher"], c["kpts"]); self.t_init["matcher_load_s"] = time.time() - t0
        t0 = time.time(); self.tiles = TileIndex(self.map, c, self.ret); self.t_init["index_s"] = time.time() - t0
        self.t_init["index_build_s"] = self.tiles.build_s; self.t_init["index_mb"] = self.tiles.size_mb
        self.ref_cache = {}
        self.last = None; self.vel = np.zeros(2); self.miss = 0; self.prev_t = None

    # -- reference features of a map patch
    def _patch(self, cx, cy):
        S = self.cfg["ref_size"]; n = self.map.shape[0]
        ox = int(np.clip(round(cx - S / 2), 0, n - S)); oy = int(np.clip(round(cy - S / 2), 0, n - S))
        return ox, oy

    def _ref_feats(self, ox, oy, cache=True):
        key = (ox, oy)
        if cache and key in self.ref_cache: return self.ref_cache[key]
        S = self.cfg["ref_size"]
        f = self.matcher.extract(self.map[oy:oy + S, ox:ox + S])
        if cache:
            if len(self.ref_cache) > 400: self.ref_cache.pop(next(iter(self.ref_cache)))
            self.ref_cache[key] = f
        return f

    def _query_feats(self, img, mask):
        f = self.matcher.extract(img)
        er = cv2.erode(mask, np.ones((15, 15), np.uint8))
        kp = f["keypoints"]; kp = (kp[0] if kp.dim() == 3 else kp).cpu().numpy()
        xi = np.clip(kp[:, 0].astype(int), 0, mask.shape[1] - 1); yi = np.clip(kp[:, 1].astype(int), 0, mask.shape[0] - 1)
        keep = er[yi, xi] > 0
        return f, keep

    def _filter(self, f, keep):
        # apply keypoint mask to a feature dict (xfeat: tensors Nx.. ; superpoint: batched)
        if "scores" in f and f["keypoints"].dim() == 2:
            return {k: (v[torch.from_numpy(keep)] if torch.is_tensor(v) and v.dim() >= 1 and v.shape[0] == len(keep) else v) for k, v in f.items()}
        m = torch.from_numpy(keep)
        out = dict(f)
        for k in ("keypoints", "keypoint_scores", "descriptors"):
            if k in f: out[k] = f[k][:, m] if k != "descriptors" else f[k][:, m]
        return out

    def _solve(self, fq, G, ox, oy, fr, h, hints_att):
        """match query feats against ref patch (origin ox,oy) and solve PnP. Returns candidate dict."""
        c = self.cfg
        t0 = time.perf_counter()
        pq, pr = self.matcher.match(fq, fr)
        tm = time.perf_counter() - t0
        out = dict(n_match=len(pq), t_match=tm, ok=False)
        if len(pq) < 8: return out
        t0 = time.perf_counter()
        img_px = apply_h(np.linalg.inv(G), np.asarray(pq, np.float64))
        obj = (np.asarray(pr, np.float64) + np.array([ox, oy])) * self.mpp
        pose = pnp_pose(obj, img_px, c["ransac_px"] * self.mpp)
        out["t_pnp"] = time.perf_counter() - t0
        if pose is None: return out
        out.update(pose=pose, n_inl=pose["n_inl"], ok=True)
        px = pose["C"][:2] / self.mpp
        out["px"] = px
        lat, lon = self.ref.px_to_latlon(px[0], px[1])
        out["lat"], out["lon"] = float(lat), float(lon)
        return out

    def _gate(self, cand, baro, att, prev_pred, dt):
        c = self.cfg; p = cand["pose"]; flags = []
        if p["n_inl"] < c["min_inl"]: flags.append("few_inliers")
        if p["res_med"] > c["max_res_px"]: flags.append("high_resid")
        if c["gate"]:
            if abs(p["height"] - baro) > max(c["gate_h_min"], c["gate_h_frac"] * baro): flags.append("height")
            if p["tilt"] > 50: flags.append("tilt_range")
            if att is not None and c["att_reliable"]:
                ti = math.degrees(math.acos(np.clip(math.cos(att[0]) * math.cos(att[1]), -1, 1)))
                if abs(p["tilt"] - ti) > c["gate_tilt_deg"]: flags.append("tilt_imu")
            if att is not None:
                dy = abs((math.degrees(p["yaw"] - att[2]) + 180) % 360 - 180)
                if dy > c["gate_yaw_deg"]: flags.append("yaw_compass")
            if prev_pred is not None:
                d = np.linalg.norm(cand["px"] - prev_pred) * self.mpp
                if d > c["track_vmax"] * dt + c["track_margin"]: flags.append("jump")
        return flags

    def locate(self, frame_gray, t, baro, att):
        """att=(roll,pitch,yaw) rad. Returns dict(accepted, lat, lon, ...)."""
        c = self.cfg; T = {}
        t0 = time.perf_counter()
        img, mask, G, cen = warp_query(frame_gray, att[2], att[0], att[1], baro, self.mpp, c["nu_size"])
        T["warp"] = time.perf_counter() - t0
        t0 = time.perf_counter()
        fq, keep = self._query_feats(img, mask); fq = self._filter(fq, keep)
        T["q_extract"] = time.perf_counter() - t0
        res = dict(accepted=False, n_inl=0, mode="none", flags="", cand=0)
        dt = (t - self.prev_t) if self.prev_t is not None else 0.2
        pred = None
        if c["tracking"] and self.last is not None and self.miss < c["track_miss_reset"]:
            pred = self.last + self.vel * dt                       # predicted nadir px
            ctr = pred + np.array([cen[0] / self.mpp, -cen[1] / self.mpp])   # predicted canvas centre in map px
            ox, oy = self._patch(*ctr)
            t0 = time.perf_counter(); fr = self._ref_feats(ox, oy, cache=False); T["r_extract"] = time.perf_counter() - t0
            cand = self._solve(fq, G, ox, oy, fr, baro, att); T["match"] = cand.get("t_match", 0); T["pnp"] = cand.get("t_pnp", 0)
            res["mode"] = "track"; res["cand"] = 1
            if cand["ok"]:
                fl = self._gate(cand, baro, att, pred, dt)
                if cand["pose"]["n_inl"] < c["track_min_inl"] and "few_inliers" not in fl: fl.append("few_inliers")
                res.update(n_inl=cand["n_inl"], flags=",".join(fl))
                if not fl:
                    return self._accept(res, cand, t, T, dt)
            self.miss += 1
            res["flags"] += ";track_fail"
        # global: retrieval
        t0 = time.perf_counter()
        S = c["q_crop"]; m = c["nu_size"] // 2
        crop = img[m - S // 2:m + S // 2, m - S // 2:m + S // 2]
        crop = cv2.resize(crop, None, fx=c["ret_scale"], fy=c["ret_scale"], interpolation=cv2.INTER_AREA)
        q = self.ret.embed([crop])[0]; T["retrieve"] = time.perf_counter() - t0
        t0 = time.perf_counter(); idx, sc = self.tiles.search(q, c["topk"]); T["faiss"] = time.perf_counter() - t0
        best = None; tm = 0; tr = 0; tp = 0; ncand = 0
        for i in idx:
            cx, cy = self.tiles.centers[i]
            ox, oy = self._patch(cx, cy)
            t0 = time.perf_counter(); fr = self._ref_feats(ox, oy); tr += time.perf_counter() - t0
            cand = self._solve(fq, G, ox, oy, fr, baro, att); ncand += 1
            tm += cand.get("t_match", 0); tp += cand.get("t_pnp", 0)
            if not cand["ok"]: continue
            fl = self._gate(cand, baro, att, None, dt)
            cand["flags"] = fl; cand["tile"] = int(i)
            if best is None or (not fl and (best["flags"] or cand["n_inl"] > best["n_inl"])) or (fl and best["flags"] and cand["n_inl"] > best["n_inl"]):
                best = cand
            if not fl and cand["n_inl"] >= c["early_inl"]: break
        T["r_extract"] = T.get("r_extract", 0) + tr; T["match"] = tm; T["pnp"] = tp
        res["mode"] = res["mode"] if res["mode"] == "track" else "global"; res["cand"] += ncand; res["tile_top1"] = int(idx[0])
        if best is not None:
            res.update(n_inl=best["n_inl"], flags=(res["flags"] + "|" if res["flags"] else "") + ",".join(best["flags"]), tile=best["tile"])
            res["raw_lat"], res["raw_lon"] = best["lat"], best["lon"]
            if not best["flags"]:
                return self._accept(res, best, t, T, dt)
        else:
            res["flags"] = (res["flags"] + "|" if res["flags"] else "") + "no_pose"
        self.miss += 1 if self.last is not None else 0
        res["T"] = T
        return res

    def _accept(self, res, cand, t, T, dt):
        px = cand["px"]
        if self.last is not None and dt > 0:
            v = (px - self.last) / dt
            self.vel = 0.5 * self.vel + 0.5 * v if self.miss == 0 else v / 2
        self.last = px.copy(); self.prev_t = t; self.miss = 0
        res.update(accepted=True, lat=cand["lat"], lon=cand["lon"], alt=float(cand["pose"]["height"]),
                   yaw=float(math.degrees(cand["pose"]["yaw"]) % 360), tilt=float(cand["pose"]["tilt"]),
                   res_med=cand["pose"]["res_med"], raw_lat=cand["lat"], raw_lon=cand["lon"], T=T)
        return res
