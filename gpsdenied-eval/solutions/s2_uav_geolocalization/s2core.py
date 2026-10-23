"""Core building blocks for S2: CAEVL retriever (tile DB + encoder) and a STREAMING port of fuse.py's causal mode.
The fusion maths (causal_consensus, fit_similarity, rotz) is imported unchanged from the author's repo/tools/fuse.py."""
import os, sys, time, importlib.util
import numpy as np, cv2, torch
from PIL import Image
from s2lib import *

CAEVL_SRC = os.path.join(HERE, 'caevl_src')
sys.path.insert(0, CAEVL_SRC)
_prev = os.getcwd()
import localizer as caevl_localizer            # author's runtime localiser (build_encoder / build_transform)

def _load_fuse():
    spec = importlib.util.spec_from_file_location('s2_fuse', os.path.join(HERE, 'repo/tools/fuse.py'))
    m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m); return m
fuse = _load_fuse()


class Retriever:
    """Canny-edge CAEVL descriptors; tile DB cut from the reference map; cosine nearest neighbour."""
    def __init__(self, weights, tile_m=120.0, stride_m=60.0, db_path=None, threads=2, log=print):
        torch.set_num_threads(threads)
        self.tile_m, self.stride_m = tile_m, stride_m
        self.encoder = caevl_localizer.build_encoder(weights, 'cpu', (256, 256))
        self.transform = caevl_localizer.build_transform((256, 256))
        img, ref, mpp = load_map(); self.mpp = mpp
        db_path = db_path or os.path.join(HERE, f'work/db_{int(tile_m)}_{int(stride_m)}.npz')
        self.build_s = 0.0
        if os.path.exists(db_path):
            z = np.load(db_path); self.desc, self.cen = z['desc'], z['cen']; self.build_s = float(z['build_s'])
        else:
            t0 = time.time()
            pts = grid_tiles(img.shape, tile_m, stride_m, mpp)
            descs = []
            B = 32
            for i in range(0, len(pts), B):
                batch = torch.stack([self.transform(Image.fromarray(cut_tile(img, x, y, tile_m, mpp))) for x, y in pts[i:i + B]])
                with torch.no_grad(): descs.append(self.encoder(batch).numpy())
                if (i // B) % 20 == 0: log(f'  db {i}/{len(pts)} {time.time() - t0:.0f}s')
            self.desc = np.concatenate(descs).astype(np.float32)
            self.desc /= np.linalg.norm(self.desc, axis=1, keepdims=True) + 1e-12
            self.cen = np.array([[x * mpp, -y * mpp] for x, y in pts])            # east, north map metres
            self.build_s = time.time() - t0
            np.savez_compressed(db_path, desc=self.desc, cen=self.cen, build_s=self.build_s, tile_m=tile_m, stride_m=stride_m)
        self.db_path = db_path
        self.index_mb = self.desc.nbytes / 1e6

    def encode(self, crop_gray):
        with torch.no_grad():
            d = self.encoder(self.transform(Image.fromarray(crop_gray)).unsqueeze(0))[0].numpy()
        return d / (np.linalg.norm(d) + 1e-12)

    def search(self, d, center_en=None, radius=None):
        t0 = time.perf_counter()
        if center_en is None or radius is None:
            sc = self.desc @ d; i = int(np.argmax(sc)); n = len(sc)
        else:
            m = np.hypot(self.cen[:, 0] - center_en[0], self.cen[:, 1] - center_en[1]) < radius
            idx = np.nonzero(m)[0]
            if len(idx) == 0:
                idx = np.arange(len(self.cen))
            sc = self.desc[idx] @ d; j = int(np.argmax(sc)); i = int(idx[j]); n = len(idx)
        return i, float(self.desc[i] @ d), n, (time.perf_counter() - t0) * 1000


class StreamFuser:
    """Streaming version of fuse.causal_mode: at each step only fixes that have arrived are used.
    Same sequence as the author's loop: buffer -> trailing-window RANSAC consensus -> accumulate inliers ->
    similarity fit on all accepted history -> aligned = s R(theta) vio + off.  (Faithful port: inliers are
    re-appended at every step, as in the original; consensus pre-filter over the whole flight (non-causal) is NOT used.)"""
    def __init__(self, cons_window=30.0, cons_thresh=60.0, cons_min=3, min_span=300.0, iters=800, consensus=True, seed=0):
        self.w, self.th, self.cm, self.span, self.iters, self.consensus = cons_window, cons_thresh, cons_min, min_span, iters, consensus
        self.rng = np.random.default_rng(seed)
        self.buf_t, self.buf_f, self.buf_v = [], [], []
        self.acc_f, self.acc_v = [], []
        self.have = None; self.ever = set(); self.n_fix = 0

    def step(self, t, vio_xy, fix_xy):
        """vio_xy: VIO position now (map metres); fix_xy: raw retrieval fix or None.  Returns (aligned_xy|None, info)."""
        arrived_ok = None
        if fix_xy is not None:
            self.buf_t.append(t); self.buf_f.append(np.asarray(fix_xy, float)); self.buf_v.append(np.asarray(vio_xy, float))
            my = len(self.buf_t) - 1
            bt = np.array(self.buf_t); idx = np.nonzero(bt >= bt[-1] - self.w)[0]
            if self.consensus:
                inl = fuse.causal_consensus(bt[idx], np.array(self.buf_f)[idx], np.array(self.buf_v)[idx],
                                            self.th, self.cm, self.iters, self.rng)
            else:
                inl = np.zeros(len(idx), bool); inl[-1] = True          # no rejection: every new fix accepted
            for a, keep in zip(idx, inl):
                if keep:
                    self.acc_f.append(self.buf_f[a]); self.acc_v.append(self.buf_v[a]); self.ever.add(int(a))
            arrived_ok = bool(inl[-1]) if len(inl) else False
            if len(self.acc_f) >= 4:
                F, V = np.array(self.acc_f), np.array(self.acc_v)
                span = np.linalg.norm(F - F.mean(0), axis=1).max()
                if span >= self.span:
                    sc, th, off = fuse.fit_similarity(V, F)
                    if 0.3 < sc < 4.0:
                        self.have = (sc, th, off)
        if self.have is None:
            return None, dict(arrived_ok=arrived_ok, n_acc=len(self.acc_f))
        sc, th, off = self.have
        return sc * (fuse.rotz(th) @ np.asarray(vio_xy, float)) + off, dict(arrived_ok=arrived_ok, n_acc=len(self.acc_f), scale=sc, yaw=th)
