"""Redundancy: combine the S6 and S5 fixes into one output stream.
modes: primary_fallback | agree_or_best | consensus (see config). Every fused row says how it was produced."""
import math, threading
from dataclasses import dataclass
from .geo import haversine_m


@dataclass
class Fused:
    t: float
    accepted: bool
    lat: float = float("nan")
    lon: float = float("nan")
    conf: float = 0.0
    source: str = ""          # engines that contributed
    quality: str = "none"     # single | agree | conflict | none
    disagree_m: float = float("nan")


class Fuser:
    def __init__(self, cfg, engines):
        f = cfg["fusion"]; self.f = f; self.engines = engines; self.last = {}; self.lock = threading.Lock()

    def update(self, fix):
        """Feed one engine fix (accepted or not); returns the Fused result for this update."""
        f = self.f
        with self.lock:
            self.last[fix.engine] = fix
            if len(self.engines) == 1:
                return Fused(fix.t, fix.accepted, fix.lat, fix.lon, fix.conf, fix.engine, "single" if fix.accepted else "none")
            prim = f["primary"]; sec = [e for e in self.engines if e != prim][0]
            a = self.last.get(prim); b = self.last.get(sec)
            fresh = lambda x: x is not None and x.accepted and abs(x.t - fix.t) <= f["max_fix_age_s"]
            pa, pb = fresh(a), fresh(b)
            if pa and pb:
                d = float(haversine_m(a.lat, a.lon, b.lat, b.lon))
                if d <= f["agree_radius_m"]:
                    wa, wb = f["weights"].get(prim, 1.0) * max(a.conf, 0.05), f["weights"].get(sec, 1.0) * max(b.conf, 0.05)
                    return Fused(fix.t, True, (a.lat * wa + b.lat * wb) / (wa + wb), (a.lon * wa + b.lon * wb) / (wa + wb), max(a.conf, b.conf), f"{prim}+{sec}", "agree", d)
                if f["mode"] == "consensus":
                    return Fused(fix.t, False, quality="conflict", source=f"{prim}+{sec}", disagree_m=d)
                best = a   # the primary engine wins a conflict ...
                if f["mode"] == "agree_or_best" and b.conf > a.conf + 0.3: best = b   # ... unless the other is clearly more confident
                return Fused(fix.t, True, best.lat, best.lon, best.conf, best.engine, "conflict", d)
            if f["mode"] == "consensus": return Fused(fix.t, False, quality="none", source="")
            one = a if pa else (b if pb else None)
            if one is None: return Fused(fix.t, False)
            if f["mode"] == "primary_fallback" and not pa and pb: return Fused(fix.t, True, b.lat, b.lon, b.conf, b.engine, "single")
            return Fused(fix.t, True, one.lat, one.lon, one.conf, one.engine, "single")
