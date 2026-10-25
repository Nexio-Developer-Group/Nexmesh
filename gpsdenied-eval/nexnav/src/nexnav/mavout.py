"""Optional injection of the fused fix into the autopilot as a second GPS (MAVLink GPS_INPUT). Disabled by default.
Velocity, altitude and speed accuracy are marked 'ignore' (the vision fixes carry position only). Configure the autopilot to accept a MAV GPS first
(ArduPilot: GPS_TYPE2 = 14, EK3_SRC1_POSXY = 3, EK3_SRC1_VELXY = 0, GPS_AUTO_SWITCH / GPS_PRIMARY as needed) and test in SITL."""
import math, time
from .geo import WGS_A

IGNORE = 1 | 8 | 16 | 32 | 128


class GpsInputSender:
    def __init__(self, cfg, telemetry, log):
        o = cfg["output"]["mavlink_gps_input"]; self.o = o; self.tel = telemetry; self.log = log; self.n = 0; self.last = 0.0
        self.min_fix = cfg["telemetry"]["gps_min_fix_type"]; self.max_age = cfg["telemetry"]["gps_max_age_s"]
        if o["connection"]:
            from pymavlink import mavutil
            self.conn = mavutil.mavlink_connection(o["connection"], source_system=cfg["telemetry"]["mavlink"]["source_system"], source_component=cfg["telemetry"]["mavlink"]["source_component"])
        else:
            self.conn = getattr(telemetry, "conn", None)
        if self.conn is None: raise RuntimeError("output.mavlink_gps_input needs a MAVLink connection (telemetry.source must be mavlink or set output.mavlink_gps_input.connection)")

    def send(self, fz, hints):
        o = self.o; now = time.monotonic()
        if not fz.accepted or now - self.last < 1.0 / o["max_rate_hz"]: return False
        if o["only_when_gps_lost"] and self.tel.gps_ok(now, self.min_fix, self.max_age): return False
        lat, lon = fz.lat, fz.lon
        if o["project_forward"]:
            age = max(0.0, min(2.0, now - fz.t))
            lat += math.degrees(hints.vn * age / WGS_A); lon += math.degrees(hints.ve * age / (WGS_A * math.cos(math.radians(lat))))
        wk_ms = int(time.time() * 1000) % 604800000
        self.conn.mav.gps_input_send(int(time.time() * 1e6), int(o["gps_id"]), IGNORE, wk_ms, 2400, 3, int(lat * 1e7), int(lon * 1e7), 0.0, 1.0, 1.0, 0.0, 0.0, 0.0, 0.0,
                                     float(o["horiz_accuracy_m"]), 0.0, 10, 0)
        self.n += 1; self.last = now
        return True
