"""S3 adapter: ShuhaievaPolina tile-matching pipeline (DINOv2-small retrieval -> ALIKED+LightGlue -> USAC_MAGSAC).
Logic mirrors UAV_Visual_Localization.ipynb (SatelliteTileDatabase, EdgeLightGlueMatcher, run_evaluation_pipeline);
tiles are kept in memory from a map resampled to the camera ground resolution."""
import os, sys, time, math
import numpy as np, cv2, torch, timm
from PIL import Image
from torchvision import transforms
from lightglue import LightGlue, ALIKED
from lightglue.utils import rbd

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "..", "harness"))
from geo import MapRef

torch.set_num_threads(int(os.environ.get("S3_THREADS", "2")))
MIN_INLIERS = 6  # notebook constant


class Adapter:
    def init(self, map_path, config):
        t0 = time.time()
        self.cfg = dict(tile=1024, overlap=0.5, topk=5, max_candidates=3, sim_threshold=0.82, early_stop=25,
                        gsd=0.1804, scale_norm=True, derotate=False, max_kp=1024)
        self.cfg.update(config)
        self.ref = MapRef(os.path.splitext(map_path)[0] + ".json")
        img = cv2.cvtColor(cv2.imread(map_path), cv2.COLOR_BGR2RGB)
        mpp = self.ref.ground_m_per_px(float(self.ref_center_lat()))
        # resample so one pixel == camera ground resolution (ratio recorded)
        self.ratio = mpp / self.cfg["gsd"] if self.cfg["scale_norm"] else 1.0
        if abs(self.ratio - 1) > 1e-3:
            img = cv2.resize(img, None, fx=self.ratio, fy=self.ratio, interpolation=cv2.INTER_CUBIC)
        self.map = img
        self.tf = transforms.Compose([transforms.Resize((518, 518)), transforms.ToTensor(),
                                      transforms.Normalize([0.485, 0.456, 0.406], [0.229, 0.224, 0.225])])
        self.dino = timm.create_model("vit_small_patch14_dinov2.lvd142m", pretrained=True, num_classes=0).eval()
        self.extractor = ALIKED(max_num_keypoints=self.cfg["max_kp"]).eval()
        self.matcher = LightGlue(features="aliked", depth_confidence=0.9, width_confidence=0.95).eval()
        self._index()
        self.startup_s = time.time() - t0

    def ref_center_lat(self):
        lat, _ = self.ref.px_to_latlon(self.ref.width / 2, self.ref.height / 2)
        return lat

    def _index(self):
        t0 = time.time()
        T, S = self.cfg["tile"], int(self.cfg["tile"] * (1 - self.cfg["overlap"]))
        h, w = self.map.shape[:2]
        cache = os.path.join(HERE, "cache", f"emb_{h}x{w}_{T}_{S}.pt")
        if os.path.exists(cache):  # descriptors are deterministic; reuse (build time of the first build is in the logs)
            d = torch.load(cache); self.tiles, self.emb = d["tiles"], d["emb"]
            self.index_build_s = d["build_s"]; self.index_mb = self.emb.numel() * 4 / 1e6; return
        self.tiles, tens = [], []
        for y in range(0, h - T + 1, S):
            for x in range(0, w - T + 1, S):
                self.tiles.append((x, y)); tens.append(self.tf(Image.fromarray(self.map[y:y + T, x:x + T])))
        embs = []
        with torch.no_grad():
            for i in range(0, len(tens), 16):
                e = self.dino(torch.stack(tens[i:i + 16])); embs.append(torch.nn.functional.normalize(e, dim=1))
        self.emb = torch.cat(embs)
        self.index_build_s = time.time() - t0
        os.makedirs(os.path.dirname(cache), exist_ok=True)
        torch.save({"tiles": self.tiles, "emb": self.emb, "build_s": self.index_build_s}, cache)
        self.index_mb = self.emb.numel() * 4 / 1e6

    def _derotate(self, frame, yaw):
        h, w = frame.shape[:2]
        M = cv2.getRotationMatrix2D((w / 2, h / 2), yaw, 1.0)  # rotate so image-up = north (yaw cw from N)
        c, s = abs(M[0, 0]), abs(M[0, 1])
        nw, nh = int(h * s + w * c), int(h * c + w * s)
        M[0, 2] += nw / 2 - w / 2; M[1, 2] += nh / 2 - h / 2
        return cv2.warpAffine(frame, M, (nw, nh), borderMode=cv2.BORDER_REFLECT)

    def step(self, frame, timestamp, hints):
        rgb = cv2.cvtColor(frame, cv2.COLOR_BGR2RGB)
        if self.cfg["derotate"] and hints:
            rgb = self._derotate(rgb, -float(hints["yaw_deg"]))  # cv2 angle>0 is counter-clockwise; north-up needs -heading
        out = dict(lat=None, lon=None, alt=None, yaw=None, conf=0.0, accepted=False, flag="", n_inliers=0, top1_sim=0.0)
        with torch.no_grad():
            q = self.dino(self.tf(Image.fromarray(rgb)).unsqueeze(0))
            q = torch.nn.functional.normalize(q, dim=1)
            sims = (q @ self.emb.T).squeeze(0)
            tk = torch.topk(sims, k=self.cfg["topk"])
        cands = [dict(x=self.tiles[i][0], y=self.tiles[i][1], score=float(s)) for i, s in zip(tk.indices.tolist(), tk.values.tolist())]
        out["top1_sim"] = cands[0]["score"]
        cl = cands[: self.cfg["max_candidates"]]
        if cl and cl[0]["score"] >= self.cfg["sim_threshold"]:
            cl = [cl[0]]
        t = torch.from_numpy(rgb).permute(2, 0, 1).float() / 255.0
        with torch.no_grad():
            f0 = self.extractor.extract(t)
        best = (0, None, None)
        T = self.cfg["tile"]
        for c in cl:
            tile = torch.from_numpy(self.map[c["y"]:c["y"] + T, c["x"]:c["x"] + T]).permute(2, 0, 1).float() / 255.0
            with torch.no_grad():
                f1 = self.extractor.extract(tile)
                m = self.matcher({"image0": f0, "image1": f1})
            f0c, f1c, mc = [rbd(x) for x in (f0, f1, m)]
            mt = mc["matches"]
            p0 = f0c["keypoints"][mt[..., 0]].cpu().numpy(); p1 = f1c["keypoints"][mt[..., 1]].cpu().numpy()
            if len(p0) < 4:
                continue
            H, mask = cv2.findHomography(p0, p1, cv2.USAC_MAGSAC, 5.0)
            if H is not None and mask.sum() > best[0]:
                best = (int(mask.sum()), H, c)
            if best[0] >= self.cfg["early_stop"]:
                break
        inl, H, tile = best
        h, w = rgb.shape[:2]
        if tile is not None and H is not None and inl >= MIN_INLIERS:
            ctr = cv2.perspectiveTransform(np.array([[[w / 2.0, h / 2.0]]], np.float32), H)[0][0]
            gx, gy = tile["x"] + ctr[0], tile["y"] + ctr[1]
            out.update(accepted=True, flag="verified", n_inliers=inl, conf=float(inl))
        else:
            tile = cands[0]; gx, gy = tile["x"] + T / 2, tile["y"] + T / 2
            out.update(accepted=False, flag="fallback_tile_centre", n_inliers=inl, conf=0.0)
        lat, lon = self.ref.px_to_latlon(gx / self.ratio, gy / self.ratio)
        out["lat"], out["lon"] = float(lat), float(lon)
        return out
