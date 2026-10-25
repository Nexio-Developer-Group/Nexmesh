"""Test engine: returns the telemetry GPS position shifted by a fixed offset (no vision). Used by the unit tests and `--engines dummy` smoke runs."""
import time
from .base import Engine, Fix
from ..geo import offset_latlon


class DummyEngine(Engine):
    name = "dummy"

    def load(self, ctx): self.ctx = ctx; self.offset = ctx.cfg.get("_dummy_offset_m", (5.0, 3.0)); self.ref = None

    def process(self, frame, hints):
        t0 = time.perf_counter()
        gps = self.ctx.cfg.get("_gps_probe")
        la, lo = gps(frame.t) if gps else (float("nan"), float("nan"))
        if la != la: return Fix(frame.t, self.name, False)
        la2, lo2 = offset_latlon(la, lo, *self.offset)
        return Fix(frame.t, self.name, True, la2, lo2, hints.alt_agl, conf=0.9, latency_s=time.perf_counter() - t0)
