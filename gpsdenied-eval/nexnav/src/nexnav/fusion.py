"""Redundancy: combine the S6 and S5 fixes into one output stream.
Fixes arrive at different times (the engines run at different speeds), so before two fixes are compared the older one is
carried forward to the newer one's time with the velocity the autopilot reported at the older frame (Fix.info vn / ve).
modes: primary_fallback | agree_or_best | consensus (see config). Every fused row says how it was produced."""
import math, threading
from dataclasses import dataclass
from .geo import haversine_m, offset_latlon


@dataclass
class Fused:
    t: float                  # time of the position (the newest contributing frame)
    accepted: bool
    lat: float = float("nan")
    lon: float = float("nan")
    conf: float = 0.0
    source: str = ""          # engines that contributed
    quality: str = "none"     # single | agree | conflict | none
    disagree_m: float = float("nan")


def _at(fix, t):
    """Fix position carried to time t with its own frame's velocity."""
    dt = t - fix.t
    if abs(dt) < 1e-6: return fix.lat, fix.lon
    return offset_latlon(fix.lat, fix.lon, fix.info.get("vn", 0.0) * dt, fix.info.get("ve", 0.0) * dt)


class Fuser:
    def __init__(self, cfg, engines):
        self.f = cfg["fusion"]; self.engines = engines; self.last = {}; self.lock = threading.Lock()

    def update(self, fix):
        """Feed one engine fix (accepted or not); returns the Fused result for this update."""
        f = self.f
        with self.lock:
            self.last[fix.engine] = fix
            if len(self.engines) == 1:
                return Fused(fix.t, fix.accepted, fix.lat, fix.lon, fix.conf, fix.engine, "single" if fix.accepted else "none")
            prim = f["primary"]; sec = [e for e in self.engines if e != prim][0]
            a = self.last.get(prim); b = self.last.get(sec)
            t_ref = max(x.t for x in (a, b) if x is not None)
            fresh = lambda x: x is not None and x.accepted and (t_ref - x.t) <= f["max_fix_age_s"]
            pa, pb = fresh(a), fresh(b)
            if pa and pb:
                la, loa = _at(a, t_ref); lb, lob = _at(b, t_ref)
                d = float(haversine_m(la, loa, lb, lob))
                if d <= f["agree_radius_m"]:
                    wa, wb = f["weights"].get(prim, 1.0) * max(a.conf, 0.05), f["weights"].get(sec, 1.0) * max(b.conf, 0.05)
                    return Fused(t_ref, True, (la * wa + lb * wb) / (wa + wb), (loa * wa + lob * wb) / (wa + wb), max(a.conf, b.conf), f"{prim}+{sec}", "agree", d)
                if f["mode"] == "consensus": return Fused(t_ref, False, quality="conflict", source=f"{prim}+{sec}", disagree_m=d)
                best, (bl, bo) = (a, (la, loa))   # the primary engine wins a conflict ...
                if f["mode"] == "agree_or_best" and b.conf > a.conf + 0.3: best, (bl, bo) = b, (lb, lob)   # ... unless the other is clearly more confident
                return Fused(t_ref, True, bl, bo, best.conf, best.engine, "conflict", d)
            if f["mode"] == "consensus": return Fused(t_ref, False)
            one = a if pa else (b if pb else None)
            if one is None: return Fused(t_ref, False)
            la_, lo_ = _at(one, t_ref)
            return Fused(t_ref, True, la_, lo_, one.conf, one.engine, "single")
