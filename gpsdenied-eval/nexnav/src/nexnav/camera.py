"""Camera sources and intrinsics. Every source yields Frame(t, image) with a monotonic timestamp (seconds).
Live sources run a capture thread that always keeps only the newest frame (engines never see a growing backlog)."""
import glob, math, os, re, threading, time
from dataclasses import dataclass
import numpy as np
import cv2


@dataclass
class Frame:
    t: float            # seconds; live: time.monotonic() at capture (minus timestamp offset); replay: recorded time
    image: np.ndarray   # BGR uint8, undistorted, flipped as configured
    index: int
    wall: float         # time.time() at capture


class Intrinsics:
    def __init__(self, width, height, fx, fy, cx, cy):
        self.width, self.height, self.fx, self.fy, self.cx, self.cy = int(width), int(height), float(fx), float(fy), float(cx), float(cy)

    @property
    def K(self):
        return np.array([[self.fx, 0, self.cx], [0, self.fy, self.cy], [0, 0, 1.0]])

    def as_dict(self):
        return dict(width=self.width, height=self.height, fx=self.fx, fy=self.fy, cx=self.cx, cy=self.cy)


def build_intrinsics(cfg, width, height):
    c = cfg
    fx = c["fx"]
    if fx is None: fx = (width / 2) / math.tan(math.radians(c["hfov_deg"] / 2))
    fy = c["fy"] or fx
    return Intrinsics(width, height, fx, fy, c["cx"] if c["cx"] is not None else width / 2, c["cy"] if c["cy"] is not None else height / 2)


def csi_pipeline(sensor_id, width, height, fps):
    return (f"nvarguscamerasrc sensor-id={sensor_id} ! video/x-raw(memory:NVMM),width={width},height={height},framerate={int(fps)}/1,format=NV12 ! "
            f"nvvidconv ! video/x-raw,format=BGRx ! videoconvert ! video/x-raw,format=BGR ! appsink drop=true max-buffers=1 sync=false")


class _Base:
    live = True

    def __init__(self, cfg):
        self.cfg = cfg; self.index = 0; self.intr = None; self._undist = None; self.dist = cfg.get("distortion")

    def _prep(self, img):
        c = self.cfg; f = c["flip"]
        if f == "h": img = cv2.flip(img, 1)
        elif f == "v": img = cv2.flip(img, 0)
        elif f == "180": img = cv2.rotate(img, cv2.ROTATE_180)
        if img.ndim == 2: img = cv2.cvtColor(img, cv2.COLOR_GRAY2BGR)
        if self.intr is None:
            h, w = img.shape[:2]; self.intr = build_intrinsics(c, w, h)
            if self.dist:
                d = np.array(self.dist, float)
                newK, _ = cv2.getOptimalNewCameraMatrix(self.intr.K, d, (w, h), 0)
                self._undist = cv2.initUndistortRectifyMap(self.intr.K, d, None, newK, (w, h), cv2.CV_16SC2)
                self.intr = Intrinsics(w, h, newK[0, 0], newK[1, 1], newK[0, 2], newK[1, 2])
        if self._undist is not None: img = cv2.remap(img, self._undist[0], self._undist[1], cv2.INTER_LINEAR)
        return img

    def open(self): raise NotImplementedError
    def read(self, timeout=1.0): raise NotImplementedError
    def close(self): pass


class LiveCamera(_Base):
    """OpenCV VideoCapture (V4L2 / GStreamer / RTSP) with a capture thread."""
    def __init__(self, cfg, spec):
        super().__init__(cfg); self.spec = spec; self.cap = None; self._lock = threading.Condition(); self._latest = None; self._run = False; self._thr = None

    def open(self):
        s = self.spec; c = self.cfg
        if s.startswith("v4l2:"):
            dev = s[5:]; dev = int(dev) if dev.isdigit() else dev
            self.cap = cv2.VideoCapture(dev, cv2.CAP_V4L2)
            self.cap.set(cv2.CAP_PROP_FRAME_WIDTH, c["width"]); self.cap.set(cv2.CAP_PROP_FRAME_HEIGHT, c["height"]); self.cap.set(cv2.CAP_PROP_FPS, c["fps"])
            self.cap.set(cv2.CAP_PROP_BUFFERSIZE, 1)
        elif s.startswith("csi:"):
            self.cap = cv2.VideoCapture(csi_pipeline(int(s[4:] or 0), c["width"], c["height"], c["fps"]), cv2.CAP_GSTREAMER)
        elif s.startswith("gst:"):
            self.cap = cv2.VideoCapture(s[4:], cv2.CAP_GSTREAMER)
        else:  # rtsp:// http:// ...
            self.cap = cv2.VideoCapture(s, cv2.CAP_FFMPEG if not s.startswith("gst") else cv2.CAP_GSTREAMER)
        if not self.cap.isOpened(): raise RuntimeError(f"cannot open camera source {s!r} (OpenCV built with GStreamer? `python -c \"import cv2;print(cv2.getBuildInformation())\"`)")
        self._run = True; self._thr = threading.Thread(target=self._loop, daemon=True); self._thr.start()

    def _loop(self):
        off = self.cfg["timestamp_offset_s"]
        while self._run:
            ok, img = self.cap.read()
            if not ok: time.sleep(0.01); continue
            fr = Frame(time.monotonic() - off, self._prep(img), self.index, time.time() - off); self.index += 1
            with self._lock: self._latest = fr; self._lock.notify_all()

    def read(self, timeout=1.0):
        with self._lock:
            if self._latest is None: self._lock.wait(timeout)
            fr, self._latest = self._latest, None
        return fr

    def close(self):
        self._run = False
        if self._thr: self._thr.join(timeout=1.0)
        if self.cap: self.cap.release()


class ReplayCamera(_Base):
    """Image folder (<seconds>.png/jpg names or fps) or a video file. realtime=True paces frames on the wall clock."""
    live = False

    def __init__(self, cfg, spec, realtime):
        super().__init__(cfg); self.spec = spec; self.realtime = realtime; self._i = 0; self._t0 = None; self.items = []; self.cap = None

    def open(self):
        r = self.cfg["replay"]; s = self.spec
        if s.startswith("folder:"):
            d = s[7:]
            files = sorted(glob.glob(os.path.join(d, "*.png")) + glob.glob(os.path.join(d, "*.jpg")) + glob.glob(os.path.join(d, "*.jpeg")))
            def tkey(p):
                m = re.match(r"^-?\d+(\.\d+)?$", os.path.splitext(os.path.basename(p))[0]); return float(m.group(0)) if m else None
            times = [tkey(p) for p in files]
            if r["timing"] == "recorded" and all(t is not None for t in times): files = [p for _, p in sorted(zip(times, files))]; times = sorted(times)
            else: times = [i / r["fps"] for i in range(len(files))]
            self.items = [(t, p) for t, p in zip(times, files) if t >= r["start_s"]][:: max(1, int(r["stride"]))]
            if not self.items: raise RuntimeError(f"no images found in {d}")
        else:
            p = s[5:] if s.startswith("file:") else s
            self.cap = cv2.VideoCapture(p)
            if not self.cap.isOpened(): raise RuntimeError(f"cannot open video {p}")
            self._fps = self.cap.get(cv2.CAP_PROP_FPS) or r["fps"]
            if r["start_s"]: self.cap.set(cv2.CAP_PROP_POS_MSEC, r["start_s"] * 1000)

    def read(self, timeout=1.0):
        r = self.cfg["replay"]
        if self.cap is None:
            if self._i >= len(self.items): return None
            t, p = self.items[self._i]; self._i += 1
            img = cv2.imread(p)
            if img is None: return self.read(timeout)
        else:
            stride = max(1, int(r["stride"]))
            for _ in range(stride - 1): self.cap.read()
            ok, img = self.cap.read()
            if not ok: return None
            t = self.cap.get(cv2.CAP_PROP_POS_MSEC) / 1000.0
        if self.realtime:
            if self._t0 is None: self._t0 = (time.monotonic(), t)
            wait = (t - self._t0[1]) - (time.monotonic() - self._t0[0])
            if wait > 0: time.sleep(wait)
        fr = Frame(t - self.cfg["timestamp_offset_s"], self._prep(img), self.index, time.time()); self.index += 1
        return fr

    def close(self):
        if self.cap: self.cap.release()


def open_camera(cfg, realtime):
    s = cfg["source"]
    cam = ReplayCamera(cfg, s, realtime) if (s.startswith("folder:") or s.startswith("file:")) else LiveCamera(cfg, s)
    cam.open()
    return cam
