"""Engine interface shared by S6 and S5 (and the test dummy)."""
from dataclasses import dataclass, field


@dataclass
class Fix:
    t: float                      # frame time
    engine: str
    accepted: bool
    lat: float = float("nan")
    lon: float = float("nan")
    alt: float = float("nan")     # estimated height (m) when the engine provides it
    yaw: float = float("nan")     # estimated heading (deg, clockwise from north) when provided
    conf: float = 0.0             # 0..1
    latency_s: float = 0.0
    raw_lat: float = float("nan")  # candidate position even when not accepted (diagnostics)
    raw_lon: float = float("nan")
    info: dict = field(default_factory=dict)


class Engine:
    name = "engine"

    def load(self, ctx):  # ctx: EngineContext (map, intrinsics, cfg, device, run_dir, log)
        raise NotImplementedError

    def process(self, frame, hints):  # -> Fix
        raise NotImplementedError

    def close(self):
        pass


@dataclass
class EngineContext:
    cfg: dict            # full resolved config
    geomap: object       # nexnav.geo.GeoMap (native resolution)
    intr: object         # nexnav.camera.Intrinsics (after undistortion)
    device: str          # "cuda" | "cpu"
    log: object          # logging.Logger
    cache_dir: str


def resolve_device(pref):
    if pref == "cpu": return "cpu"
    try:
        import torch
        if torch.cuda.is_available(): return "cuda"
    except Exception:
        pass
    if pref == "cuda": raise RuntimeError("engines.device = cuda but torch.cuda is not available")
    return "cpu"
