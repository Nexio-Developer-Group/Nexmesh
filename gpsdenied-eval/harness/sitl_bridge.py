"""Adapter output -> MAVLink GPS_INPUT bridge (runbook section 11).
Accepted estimates only; velocity fields ignored; configurable delay; projects each fix forward to the send
time with the EKF velocity (as S1 does). Logs every message sent."""
import json, math, time
from pymavlink import mavutil

R = 6378137.0
IGNORE = 1 | 2 | 4 | 8 | 16 | 32 | 128  # alt, hdop, vdop, vel_h, vel_v, speed_acc, vert_acc (keep horizontal accuracy)


class SitlBridge:
    def __init__(self, mav, log_path, gps_id=0, default_acc=25.0):
        self.m, self.gps_id, self.default_acc = mav, gps_id, default_acc
        self.log = open(log_path, "w"); self.n = 0

    def send(self, t_sim, lat, lon, conf=None, vel_ne=None, age_s=0.0, tag=""):
        """t_sim: SITL boot seconds at send time. age_s: time since the frame was captured (latency)."""
        if vel_ne is not None and age_s > 0:
            lat += math.degrees(vel_ne[0] * age_s / R)
            lon += math.degrees(vel_ne[1] * age_s / (R * math.cos(math.radians(lat))))
        acc = self.default_acc if conf is None else float(min(max(self.default_acc * 2.0 / (1 + conf / 10.0), 5.0), 100.0))
        now_us = int(t_sim * 1e6)
        wk_ms = int(t_sim * 1000) % 604800000
        # args: usec, gps_id, ignore, week_ms, week, fix_type, lat, lon, alt, hdop, vdop, vn, ve, vd,
        #       speed_acc, horiz_acc, vert_acc, satellites, yaw   (week must be > 0 for the driver to accept)
        self.m.mav.gps_input_send(now_us, self.gps_id, IGNORE, wk_ms, 2400, 3, int(lat * 1e7), int(lon * 1e7), 0.0,
                                  1.0, 1.0, 0.0, 0.0, 0.0, 0.0, acc, 0.0, 10, 0)
        self.n += 1
        self.log.write(json.dumps({"t": t_sim, "lat": lat, "lon": lon, "acc": acc, "age": age_s, "tag": tag}) + "\n")

    def close(self):
        self.log.close()
