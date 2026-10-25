"""Integration check of the LIVE path against ArduCopter SITL (not a pytest): MAVLink telemetry in, run folder out, GPS_INPUT injection back.
The vehicle hovers at the start point A of the recorded flight (200 m, heading 0); the camera replays that frame, so the S6 fix must equal the SITL position.
Phases: (1) real GPS healthy: estimates are compared with GPS_RAW; (2) GPS cut with SIM_GPS_DISABLE: nexnav injects GPS_INPUT (second GPS) and the EKF keeps its position.
Usage: python sitl_live_check.py --ardupilot /tmp/ardupilot --frame data/nidar/frames/0.000.png --map data/nidar/map_esri_z19.png --out /tmp/live"""
import argparse, json, os, shutil, subprocess, sys, time
import cv2
from pymavlink import mavutil

ap = argparse.ArgumentParser()
ap.add_argument("--ardupilot", default="/tmp/ardupilot"); ap.add_argument("--frame", required=True); ap.add_argument("--map", required=True)
ap.add_argument("--out", default="/tmp/live"); ap.add_argument("--seconds", type=int, default=60); ap.add_argument("--cut-at", type=int, default=25)
ap.add_argument("--nexnav", default=shutil.which("nexnav") or "nexnav"); ap.add_argument("--home", default="18.51770505,73.85385786")
ap.add_argument("--extra", nargs="*", default=[])
a = ap.parse_args()
os.makedirs(a.out, exist_ok=True); wd = os.path.join(a.out, "sitl"); os.makedirs(wd, exist_ok=True)
fr = os.path.join(a.out, "frames"); os.makedirs(fr, exist_ok=True)
img = cv2.imread(a.frame)
for k in range(a.seconds + 20): cv2.imwrite(os.path.join(fr, f"{k:.3f}.png"), img)
open(os.path.join(wd, "extra.parm"), "w").write("ARMING_CHECK 0\nGPS_TYPE2 14\nGPS_AUTO_SWITCH 0\nGPS_PRIMARY 0\nEK3_GPS_CHECK 0\nEK3_SRC1_POSXY 3\nEK3_SRC1_VELXY 0\nEK3_SRC1_POSZ 1\nEK3_SRC1_VELZ 0\nLOG_DISARMED 0\n")
lat, lon = a.home.split(",")
sitl = subprocess.Popen([os.path.join(a.ardupilot, "build/sitl/bin/arducopter"), "--model", "+", "--speedup", "1", "--home", f"{lat},{lon},560,0", "--defaults",
                         os.path.join(a.ardupilot, "Tools/autotest/default_params/copter.parm") + "," + os.path.join(wd, "extra.parm"), "-I", "0"],
                        cwd=wd, stdout=open(os.path.join(wd, "sitl.log"), "w"), stderr=subprocess.STDOUT)
nex = None
try:
    time.sleep(4)
    m = None
    for _ in range(30):
        try: m = mavutil.mavlink_connection("tcp:127.0.0.1:5760"); break
        except Exception: time.sleep(1)
    m.wait_heartbeat(timeout=30); print("SITL up", flush=True)
    for mid in (33, 24): m.mav.command_long_send(m.target_system, m.target_component, 511, 0, mid, 200000, 0, 0, 0, 0, 0)
    texts = []
    def pump(tmax=0.2):
        msg = m.recv_match(blocking=True, timeout=tmax)
        while msg is not None:
            if msg.get_type() == "STATUSTEXT": texts.append(msg.text)
            msg = m.recv_match(blocking=False)
    t0 = time.time()
    while not any("is using GPS" in x for x in texts) and time.time() - t0 < 120: pump()
    m.mav.set_mode_send(m.target_system, mavutil.mavlink.MAV_MODE_FLAG_CUSTOM_MODE_ENABLED, m.mode_mapping()["GUIDED"])
    armed = False; t0 = time.time()
    while time.time() - t0 < 90:
        m.mav.command_long_send(m.target_system, m.target_component, 400, 0, 1, 21196, 0, 0, 0, 0, 0)
        m.mav.command_long_send(m.target_system, m.target_component, 22, 0, 0, 0, 0, 0, 0, 0, 200)
        pump(0.5); g = m.messages.get("GLOBAL_POSITION_INT")
        if g is not None and g.relative_alt / 1000.0 > 195: break
    print("hovering at", m.messages["GLOBAL_POSITION_INT"].relative_alt / 1000.0, "m", flush=True)
    cfg = {"run": {"name": "sitl_live", "engines": ["s6"], "output_dir": os.path.join(a.out, "runs"), "realtime": True, "max_seconds": a.seconds, "progress_every_s": 5},
           "camera": {"source": f"folder:{fr}", "hfov_deg": 60.0, "mount": "nadir_stabilised"}, "map": {"path": os.path.abspath(a.map), "cache_dir": os.path.join(a.out, "cache")},
           "telemetry": {"source": "mavlink", "mavlink": {"connection": "tcp:127.0.0.1:5762"}, "altitude": {"source": "relative_alt", "min_m": 30, "max_m": 800}},
           "engines": {"device": "cpu", "third_party_dir": os.path.abspath(os.path.join(os.path.dirname(a.map), "..", "..", "solutions", "s6_jetson_composite", "repo"))},
           "output": {"mavlink_gps_input": {"enabled": True, "gps_id": 1, "only_when_gps_lost": True}}}
    import yaml
    p = os.path.join(a.out, "live.yaml"); yaml.safe_dump(cfg, open(p, "w"))
    nex = subprocess.Popen([a.nexnav, "run", "-c", p] + a.extra, stdout=open(os.path.join(a.out, "nexnav.log"), "w"), stderr=subprocess.STDOUT)
    t_start = time.time(); cut = False; ekf = []
    while nex.poll() is None:
        pump(0.2)
        if not cut and time.time() - t_start > a.cut_at:
            m.mav.param_set_send(m.target_system, m.target_component, b"SIM_GPS_DISABLE", 1, 9); m.mav.param_set_send(m.target_system, m.target_component, b"GPS_PRIMARY", 1, 2); cut = True
            print("GPS cut at +%ds" % a.cut_at, flush=True)
        g = m.messages.get("GLOBAL_POSITION_INT")
        if g is not None: ekf.append((time.time() - t_start, g.lat / 1e7, g.lon / 1e7, g.relative_alt / 1000.0))
        g2 = m.messages.get("GPS2_RAW")
    gps2 = m.messages.get("GPS2_RAW")
    json.dump({"ekf_trace": ekf[::10], "statustext": texts[-15:], "gps2_raw_seen": gps2 is not None and gps2.fix_type, "gps_cut_at_s": a.cut_at}, open(os.path.join(a.out, "sitl_side.json"), "w"), indent=1)
    print("nexnav exit", nex.returncode)
finally:
    if nex and nex.poll() is None: nex.terminate()
    sitl.terminate()
