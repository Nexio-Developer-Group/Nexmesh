"""Telemetry: attitude, height above ground, odometry velocity and the real GPS (for comparison only).
Sources: mavlink (live), csv (replay), fixed (bench). All expose .at(t) -> Hints and .gps_at(t) -> GpsSample|None."""
import bisect, csv, math, threading, time
from dataclasses import dataclass, field
import numpy as np


@dataclass
class Hints:
    ok: bool                 # attitude + altitude usable
    roll: float = 0.0        # rad
    pitch: float = 0.0       # rad
    yaw: float = 0.0         # rad, compass (0 = north, clockwise)
    alt_agl: float = 0.0     # m (after offset)
    vn: float = 0.0          # m/s EKF/GPS velocity
    ve: float = 0.0
    vu: float = 0.0
    age_s: float = 0.0
    reason: str = ""


@dataclass
class GpsSample:
    t: float
    lat: float
    lon: float
    fix_type: int
    eph_m: float = float("nan")
    sats: int = -1


class TelemetryBase:
    def start(self): pass
    def stop(self): pass
    def at(self, t): raise NotImplementedError
    def gps_at(self, t): return None
    def gps_ok(self, t, min_fix=3, max_age=1.0): return False
    def gps_age(self, sample, t): return 0.0     # seconds between a GPS sample and frame time t
    def log_rows(self): return []


class FixedTelemetry(TelemetryBase):
    def __init__(self, cfg):
        f = cfg["telemetry"]["fixed"]; self.f = f; self.off = cfg["telemetry"]["altitude"]["offset_m"]

    def at(self, t):
        return Hints(True, 0, 0, math.radians(self.f["yaw_deg"]), self.f["alt_agl_m"] + self.off)

    def gps_at(self, t):
        return GpsSample(t, self.f["lat"], self.f["lon"], 3) if self.f["lat"] is not None else None

    def gps_ok(self, t, min_fix=3, max_age=1.0): return self.f["lat"] is not None


class CsvTelemetry(TelemetryBase):
    """Columns: t, lat, lon, alt, yaw [, roll, pitch, vn, ve, vu]. yaw/roll/pitch in degrees; alt = height above ground (m).
    If vn/ve/vu are absent they are derived from the lat/lon/alt differences (replay only)."""
    def __init__(self, cfg):
        tc = cfg["telemetry"]; path = tc["csv"]["path"]; self.cfg = cfg; self.off = tc["altitude"]["offset_m"]
        rows = list(csv.DictReader(open(path)))
        g = lambda k, default=None: np.array([float(r[k]) if r.get(k) not in (None, "", "None") else np.nan for r in rows]) if k in rows[0] else (np.full(len(rows), default) if default is not None else None)
        self.t = g("t"); self.lat = g("lat"); self.lon = g("lon"); self.alt = g("alt"); self.yaw = g("yaw")
        self.roll = g("roll", 0.0); self.pitch = g("pitch", 0.0)
        self.vn, self.ve, self.vu = g("vn"), g("ve"), g("vu")
        if self.vn is None:
            lat0 = self.lat[0]; N = np.radians(self.lat - lat0) * 6378137.0; E = np.radians(self.lon - self.lon[0]) * 6378137.0 * math.cos(math.radians(lat0))
            self.vn, self.ve, self.vu = np.gradient(N, self.t), np.gradient(E, self.t), np.gradient(self.alt, self.t)
        self.fixed_alt = tc["altitude"]["source"] == "fixed"; self.fixed_m = tc["altitude"]["fixed_m"]
        self._yaw_unwrapped = np.unwrap(np.radians(self.yaw))

    def _i(self, a, t): return float(np.interp(t, self.t, a))

    def at(self, t):
        if t < self.t[0] - 1.0 or t > self.t[-1] + 1.0: return Hints(False, reason="outside telemetry file")
        alt = (self.fixed_m if self.fixed_alt else self._i(self.alt, t)) + self.off
        return Hints(True, math.radians(self._i(self.roll, t)), math.radians(self._i(self.pitch, t)), float(np.interp(t, self.t, self._yaw_unwrapped)) % (2 * math.pi),
                     alt, self._i(self.vn, t), self._i(self.ve, t), self._i(self.vu, t))

    def gps_at(self, t):
        if t < self.t[0] or t > self.t[-1]: return None
        return GpsSample(t, self._i(self.lat, t), self._i(self.lon, t), 3)

    def gps_ok(self, t, min_fix=3, max_age=1.0): return self.t[0] <= t <= self.t[-1]
    def gps_age(self, sample, t): return abs(t - sample.t)


class MavlinkTelemetry(TelemetryBase):
    """Reads ATTITUDE, GLOBAL_POSITION_INT, GPS_RAW_INT, VFR_HUD, DISTANCE_SENSOR from the autopilot in a background thread."""
    def __init__(self, cfg, conn=None):
        self.cfg = cfg; tc = cfg["telemetry"]; self.mc = tc["mavlink"]; self.alt_cfg = tc["altitude"]; self.gps_ref = tc["gps_reference"]
        self.max_age = tc["max_age_s"]; self.conn = conn; self._lock = threading.Lock(); self._run = False; self._thr = None
        self.att = None; self.gpi = None; self.gps_raw = None; self.rng = None; self.hud = None
        self.gps_hist = []; self.rows = []; self._last_log = 0.0
        self._log_dt = 1.0 / max(cfg["output"]["telemetry_log_hz"], 0.1)

    def start(self):
        from pymavlink import mavutil
        if self.conn is None:
            self.conn = mavutil.mavlink_connection(self.mc["connection"], source_system=self.mc["source_system"], source_component=self.mc["source_component"],
                                                   baud=int(self.mc["connection"].rsplit(":", 1)[-1]) if self.mc["connection"].startswith("serial:") and self.mc["connection"].rsplit(":", 1)[-1].isdigit() else 115200)
        if not self.conn.wait_heartbeat(timeout=self.mc["heartbeat_timeout_s"]): raise RuntimeError("no MAVLink heartbeat: check telemetry.mavlink.connection")
        hz = max(1, int(self.mc["stream_rate_hz"]))
        for mid in (30, 33, 24, 74, 132):  # ATTITUDE, GLOBAL_POSITION_INT, GPS_RAW_INT, VFR_HUD, DISTANCE_SENSOR
            self.conn.mav.command_long_send(self.conn.target_system, self.conn.target_component, 511, 0, mid, int(1e6 / hz), 0, 0, 0, 0, 0)
        self._run = True; self._thr = threading.Thread(target=self._loop, daemon=True); self._thr.start()

    def _loop(self):
        while self._run:
            m = self.conn.recv_match(blocking=True, timeout=0.5)
            if m is None: continue
            now = time.monotonic(); ty = m.get_type()
            with self._lock:
                if ty == "ATTITUDE": self.att = (now, m.roll, m.pitch, m.yaw)
                elif ty == "GLOBAL_POSITION_INT": self.gpi = (now, m.lat / 1e7, m.lon / 1e7, m.alt / 1000.0, m.relative_alt / 1000.0, m.vx / 100.0, m.vy / 100.0, m.vz / 100.0, m.hdg)
                elif ty == "GPS_RAW_INT":
                    self.gps_raw = GpsSample(now, m.lat / 1e7, m.lon / 1e7, m.fix_type, (m.eph / 100.0) if m.eph != 65535 else float("nan"), m.satellites_visible)
                    if self.gps_ref == "gps_raw": self._push_gps(self.gps_raw)
                elif ty == "VFR_HUD": self.hud = (now, m.alt, m.heading)
                elif ty == "DISTANCE_SENSOR": self.rng = (now, m.current_distance / 100.0)
                if ty == "GLOBAL_POSITION_INT" and self.gps_ref == "global_position":
                    self._push_gps(GpsSample(now, m.lat / 1e7, m.lon / 1e7, 3))
                if now - self._last_log >= self._log_dt and self.att is not None:
                    self._last_log = now; g = self.gpi
                    self.rows.append(dict(t=round(now, 3), roll_deg=math.degrees(self.att[1]), pitch_deg=math.degrees(self.att[2]), yaw_deg=math.degrees(self.att[3]) % 360,
                                          rel_alt_m=g[4] if g else float("nan"), ekf_lat=g[1] if g else float("nan"), ekf_lon=g[2] if g else float("nan"),
                                          vn=g[5] if g else float("nan"), ve=g[6] if g else float("nan"), vd=g[7] if g else float("nan"),
                                          gps_fix=self.gps_raw.fix_type if self.gps_raw else -1, gps_lat=self.gps_raw.lat if self.gps_raw else float("nan"),
                                          gps_lon=self.gps_raw.lon if self.gps_raw else float("nan"), gps_sats=self.gps_raw.sats if self.gps_raw else -1))

    def _push_gps(self, s):
        self.gps_hist.append(s)
        if len(self.gps_hist) > 600: self.gps_hist = self.gps_hist[-400:]

    def stop(self):
        self._run = False
        if self._thr: self._thr.join(timeout=1.0)

    def at(self, t):
        now = time.monotonic()
        with self._lock: att, gpi, rng, hud = self.att, self.gpi, self.rng, self.hud
        if att is None: return Hints(False, reason="no ATTITUDE yet")
        age = now - att[0]
        if age > self.max_age: return Hints(False, age_s=age, reason="stale ATTITUDE")
        src = self.alt_cfg["source"]
        if src == "relative_alt":
            if gpi is None: return Hints(False, reason="no GLOBAL_POSITION_INT")
            alt = gpi[4]
        elif src == "rangefinder":
            if rng is None: return Hints(False, reason="no DISTANCE_SENSOR")
            alt = rng[1]
        elif src == "vfr_hud":
            if hud is None: return Hints(False, reason="no VFR_HUD")
            alt = hud[1]
        else: alt = self.alt_cfg["fixed_m"]
        vn = ve = vu = 0.0
        if gpi is not None: vn, ve, vu = gpi[5], gpi[6], -gpi[7]
        return Hints(True, att[1], att[2], att[3] % (2 * math.pi), alt + self.alt_cfg["offset_m"], vn, ve, vu, age)

    def gps_at(self, t):
        with self._lock:
            if self.gps_ref == "none" or not self.gps_hist: return None
            return self.gps_hist[-1]

    def gps_ok(self, t, min_fix=3, max_age=1.0):
        with self._lock: g = self.gps_raw
        return g is not None and g.fix_type >= min_fix and (time.monotonic() - g.t) <= max_age

    def gps_age(self, sample, t): return max(0.0, time.monotonic() - sample.t)

    def log_rows(self): return self.rows


def make_telemetry(cfg):
    s = cfg["telemetry"]["source"]
    return {"mavlink": MavlinkTelemetry, "csv": CsvTelemetry, "fixed": FixedTelemetry}[s](cfg)
