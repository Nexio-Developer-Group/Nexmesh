"""Closed-loop ArduCopter SITL scenarios SIM-0..SIM-7 (runbook section 11), Mode B (video in the loop):
the vehicle flies A->B->C->D (+RTL for SIM-7); at each recorded frame position along the NIDAR path the
solution's precomputed estimate for that frame (estimates.csv from the offline replay) is passed through the
bridge to a second GPS (GPS_INPUT). This is NOT fully closed loop: frames cannot show positions the recorded
aircraft never visited, and estimates are not recomputed from the SITL vehicle's own view."""
import argparse, csv, json, math, os, subprocess, sys, time
import numpy as np
from pymavlink import mavutil
sys.path.insert(0, os.path.dirname(__file__))
from sitl_bridge import SitlBridge

R = 6378137.0
SCEN = {  # id -> options
    "SIM-0": dict(cut=None, feed=False), "SIM-1": dict(cut=60, feed=False), "SIM-2": dict(cut=60, feed=True),
    "SIM-3": dict(cut=60, feed=True, drop=[(80, 100), (125, 145)]),
    "SIM-4a": dict(cut=60, feed=True, delay=0.2), "SIM-4b": dict(cut=60, feed=True, delay=0.5),
    "SIM-5": dict(cut=60, feed=True, outliers=0.05),
    "SIM-6": dict(cut=0, feed=True), "SIM-7": dict(cut=60, feed=True, rtl=True),
}
EXTRA_PARM = """ARMING_CHECK 0
LOG_BITMASK 4094
LOG_DISARMED 0
GPS_TYPE2 14
GPS_AUTO_SWITCH 0
GPS_PRIMARY 0
EK3_GPS_CHECK 0
EK3_SRC1_POSXY 3
EK3_SRC1_VELXY 0
EK3_SRC1_POSZ 1
EK3_SRC1_VELZ 0
WPNAV_SPEED 1200
WPNAV_ACCEL 250
"""


def dist(a, b):
    dn = math.radians(b[0] - a[0]) * R; de = math.radians(b[1] - a[1]) * R * math.cos(math.radians(a[0]))
    return math.hypot(dn, de)


def offset(lat, lon, e, n):
    return lat + math.degrees(n / R), lon + math.degrees(e / (R * math.cos(math.radians(lat))))


def load_est(path):
    rows = list(csv.DictReader(open(path)))
    return [(float(r["t"]), float(r["lat"]), float(r["lon"]), float(r["conf"] or 0), r["accepted"] in ("True", "true", "1")) for r in rows]


def run(scn, est_csv, truth_csv, mission_json, seed, out, ardupilot="/tmp/ardupilot", speedup=8, port=5760):
    o = SCEN[scn]; os.makedirs(out, exist_ok=True)
    rng = np.random.default_rng(seed)
    mis = json.load(open(mission_json)); wps = {k: tuple(v) for k, v in mis["waypoints"].items()}
    agl = mis["agl_m"]; speed = mis["cruise_mps"]
    est = load_est(est_csv)
    tr = list(csv.DictReader(open(truth_csv)))
    tt = np.array([float(r["t"]) for r in tr]); tlat = np.array([float(r["lat"]) for r in tr]); tlon = np.array([float(r["lon"]) for r in tr])
    wd = os.path.join(out, "sitl_wd"); os.makedirs(wd, exist_ok=True)
    open(os.path.join(wd, "extra.parm"), "w").write(EXTRA_PARM)
    parm = os.path.join(ardupilot, "Tools/autotest/default_params/copter.parm")
    A = wps["A"]
    proc = subprocess.Popen([os.path.join(ardupilot, "build/sitl/bin/arducopter"), "--model", "+", "--speedup", str(speedup),
                             "--home", f"{A[0]},{A[1]},560,0", "--defaults", f"{parm},{os.path.join(wd, 'extra.parm')}", "-I0",
                             "--base-port", str(port - 5760 + 5760)] if False else
                            [os.path.join(ardupilot, "build/sitl/bin/arducopter"), "--model", "+", "--speedup", str(speedup),
                             "--home", f"{A[0]},{A[1]},560,0", "--defaults", f"{parm},{os.path.join(wd, 'extra.parm')}", "-I", str(port - 5760) if port != 5760 else "0"],
                            cwd=wd, stdout=open(os.path.join(wd, "sitl.log"), "w"), stderr=subprocess.STDOUT)
    rows, texts, tlog = [], [], []
    try:
        time.sleep(3); m = None
        for _ in range(30):
            try: m = mavutil.mavlink_connection(f"tcp:127.0.0.1:{port}"); break
            except Exception: time.sleep(1)
        m.wait_heartbeat(timeout=30)
        for mid, hz in [(33, 20), (164, 20), (193, 4)]:
            m.mav.command_long_send(m.target_system, m.target_component, 511, 0, mid, int(1e6 / hz), 0, 0, 0, 0, 0)
        st = dict(t=0.0, ekf=None, tru=None, hb=None, vel=(0.0, 0.0))
        bridge = SitlBridge(m, os.path.join(out, "bridge_log.jsonl"))

        def pump(block=0.02):
            msg = m.recv_match(blocking=True, timeout=block)
            while msg is not None:
                ty = msg.get_type()
                if ty == "GLOBAL_POSITION_INT":
                    st["t"] = msg.time_boot_ms / 1000.0; st["ekf"] = (msg.lat / 1e7, msg.lon / 1e7); st["vel"] = (msg.vx / 100.0, msg.vy / 100.0)
                    st["rel"] = msg.relative_alt / 1000.0
                elif ty == "SIMSTATE": st["tru"] = (msg.lat / 1e7, msg.lng / 1e7)
                elif ty == "HEARTBEAT" and msg.get_srcSystem() == m.target_system: st["hb"] = (msg.base_mode, msg.custom_mode)
                elif ty == "STATUSTEXT": texts.append((st["t"], msg.text)); print("[sitl]", round(st["t"], 1), msg.text, flush=True)
                elif ty == "EKF_STATUS_REPORT": st["flags"] = msg.flags
                msg = m.recv_match(blocking=False)

        def wait(cond, timeout, what):
            t0 = time.time()
            while time.time() - t0 < timeout:
                pump()
                if cond(): return
            raise RuntimeError("timeout: " + what)

        wait(lambda: st["ekf"] and st["tru"], 40, "state")
        # find the GPS-disable param name for this build
        for name in ("SIM_GPS_DISABLE", "SIM_GPS1_DISABLE"):
            m.mav.param_request_read_send(m.target_system, m.target_component, name.encode(), -1)
            t0 = time.time(); got = None
            while time.time() - t0 < 2 and got is None:
                msg = m.recv_match(type="PARAM_VALUE", blocking=True, timeout=0.5)
                if msg is not None and msg.param_id.strip("\x00") == name: got = name
            if got: gps_param = got; break
        else:
            raise RuntimeError("no GPS disable param")
        wait(lambda: any("is using GPS" in x[1] for x in texts), 1800, "EKF using GPS")
        for _ in range(100):  # GUIDED is refused until the EKF has a position, so request it now and confirm
            m.mav.set_mode_send(m.target_system, mavutil.mavlink.MAV_MODE_FLAG_CUSTOM_MODE_ENABLED, m.mode_mapping()["GUIDED"])
            for _ in range(10): pump(0.05)
            if st["hb"] and st["hb"][1] == 4: break
        else: raise RuntimeError("GUIDED mode not accepted")
        tk_last = -99.0; arm_last = -99.0

        def takeoff_done():  # arm and take off together: the autopilot auto-disarms 10 s (sim) after arming
            nonlocal tk_last, arm_last
            armed = bool(st["hb"] and st["hb"][0] & 128)
            if not armed and st["t"] - arm_last > 1.0:
                m.mav.command_long_send(m.target_system, m.target_component, 400, 0, 1, 21196, 0, 0, 0, 0, 0); arm_last = st["t"]
            elif armed and st.get("rel", 0) < 3 and st["t"] - tk_last > 1.0:
                m.mav.command_long_send(m.target_system, m.target_component, 22, 0, 0, 0, 0, 0, 0, 0, agl); tk_last = st["t"]
            return st.get("rel", 0) > agl - 3
        wait(takeoff_done, 900, "takeoff")
        print("at altitude", st.get("rel"), flush=True)
        mask = 0b0000111111111000

        def goto(p):
            m.mav.set_position_target_global_int_send(0, m.target_system, m.target_component, 6, mask, int(p[0] * 1e7), int(p[1] * 1e7), agl, 0, 0, 0, 0, 0, 0, 0, 0)

        tA = None; cut_done = False; last_idx = 0; sent = 0; legs = ["A", "B", "C", "D"]; leg = 0
        goto(wps["A"]); pending = []; arrivals = {}; failsafe_t = None; ends = None
        t_wall0 = time.time(); last_goto = 0.0; rtl = False
        while time.time() - t_wall0 < 3600:
            pump()
            tsim = st["t"]; tru = st["tru"]; ekf = st["ekf"]
            if tru is None: continue
            if tA is None:
                if dist(tru, wps["A"]) < 4.0:
                    tA = tsim; arrivals["A"] = 0.0; leg = 1; goto(wps["B"])
                    if o["cut"] == 0:
                        m.mav.param_set_send(m.target_system, m.target_component, gps_param.encode(), 1, 9); cut_done = True
                        if o["feed"]: m.mav.param_set_send(m.target_system, m.target_component, b"GPS_PRIMARY", 1, 2)
                continue
            mt = tsim - tA
            if not rtl and tsim - last_goto > 4 and leg < 4:
                goto(wps[legs[leg]]); last_goto = tsim
            if leg < 4 and dist(tru, wps[legs[leg]]) < 4.0:
                arrivals[legs[leg]] = mt; leg += 1
                if leg < 4: goto(wps[legs[leg]])
                elif o.get("rtl"):
                    m.mav.set_mode_send(m.target_system, mavutil.mavlink.MAV_MODE_FLAG_CUSTOM_MODE_ENABLED, m.mode_mapping()["RTL"]); rtl = True
                else:
                    ends = mt
            if o["cut"] is not None and not cut_done and mt >= o["cut"]:
                m.mav.param_set_send(m.target_system, m.target_component, gps_param.encode(), 1, 9); cut_done = True
                if o["feed"]: m.mav.param_set_send(m.target_system, m.target_component, b"GPS_PRIMARY", 1, 2)
                tlog.append(("gps_cut", mt))
            # frame position along the recorded path -> estimate for that frame
            lo = max(last_idx - 3, 0); hi = min(last_idx + 60, len(tt) - 1)
            d = np.hypot((tlat[lo:hi + 1] - tru[0]) * 111320.0, (tlon[lo:hi + 1] - tru[1]) * 111320.0 * math.cos(math.radians(tru[0])))
            idx = lo + int(np.argmin(d))
            on_path = (leg <= 4 and not rtl and d.min() < 150)
            if idx > last_idx and on_path:
                for k in range(last_idx + 1, idx + 1):
                    e = est[k] if k < len(est) else None
                    if e and e[4] and (o["feed"]) and cut_done:
                        te = mt
                        if any(a <= te <= b for a, b in o.get("drop", [])): continue
                        # Mode B: transplant the solution's error vector for this recorded frame onto the vehicle's
                        # true position (the recorded frame position differs from the SITL vehicle by its cross-track offset)
                        lat, lon = tru[0] + (e[1] - tlat[k]), tru[1] + (e[2] - tlon[k]); tag = "ok"
                        if o.get("outliers") and rng.random() < o["outliers"]:
                            br = rng.uniform(0, 2 * math.pi); dm = rng.uniform(100, 300)
                            lat, lon = offset(lat, lon, dm * math.sin(br), dm * math.cos(br)); tag = "outlier"
                        pending.append((tsim + o.get("delay", 0.0), lat, lon, e[3], tsim, tag, e[0]))
                last_idx = idx
            while pending and pending[0][0] <= tsim:
                ts, lat, lon, conf, tcap, tag, te = pending.pop(0)
                bridge.send(tsim, lat, lon, conf, st["vel"], age_s=tsim - tcap, tag=tag); sent += 1
            err = dist(ekf, tru)
            if len(rows) == 0 or tsim - rows[-1][0] >= 0.1:
                rows.append((round(tsim, 3), round(mt, 3), tru[0], tru[1], ekf[0], ekf[1], round(err, 2), st["hb"][1] if st["hb"] else -1, st.get("flags", -1)))
            if ends is not None and mt - ends > 3: break
            if rtl and st["hb"] and not (st["hb"][0] & 128) and mt - arrivals.get("D", 0) > 5: break
            if rtl and mt - arrivals.get("D", mt) > 400: break
            if st["hb"] and st["hb"][1] == 9 and not rtl and cut_done:  # LAND
                failsafe_t = failsafe_t or mt
                if mt - failsafe_t > 5: break
            if mt > 500: break
        # parameter dump
        m.mav.param_request_list_send(m.target_system, m.target_component)
        pars = {}; t0 = time.time()
        while time.time() - t0 < 6:
            msg = m.recv_match(type="PARAM_VALUE", blocking=True, timeout=0.5)
            if msg is not None: pars[msg.param_id.strip("\x00")] = msg.param_value
        json.dump(pars, open(os.path.join(out, "params.json"), "w"))
        bridge.close()
    finally:
        proc.terminate()
        json.dump(texts, open(os.path.join(out, "statustext_all.json"), "w"))
    with open(os.path.join(out, "ekf_vs_truth.csv"), "w", newline="") as f:
        w = csv.writer(f); w.writerow(["t_sim", "t_mission", "true_lat", "true_lon", "ekf_lat", "ekf_lon", "err_m", "mode", "ekf_flags"]); w.writerows(rows)
    arr = np.array([(r[1], r[6]) for r in rows]) if rows else np.zeros((0, 2))
    cut = o["cut"] if o["cut"] is not None else 0
    after = arr[arr[:, 0] >= cut] if len(arr) else arr
    path = [(tlat[i], tlon[i]) for i in range(len(tt))]
    maxdev = 0.0
    for r in rows:
        if r[1] > 0:
            dd = min(dist((r[2], r[3]), p) for p in path[::5]) if r[1] < (ends or 1e9) + 1 else 0
            maxdev = max(maxdev, dd)
    summary = dict(scenario=scn, seed=seed, speedup=speedup, gps_cut_t=o["cut"], fixes_sent=sent, arrivals=arrivals,
                   max_ekf_err_m=float(after[:, 1].max()) if len(after) else None, mean_ekf_err_m=float(after[:, 1].mean()) if len(after) else None,
                   final_ekf_err_m=float(arr[-1, 1]) if len(arr) else None, failsafe_or_land_t=failsafe_t,
                   statustext=[t for t in texts if any(w in t[1].lower() for w in ("failsafe", "ekf", "land", "gps", "glitch"))][:40],
                   mission_completed=("D" in arrivals), max_path_deviation_m=maxdev,
                   dist_home_end_m=(dist((rows[-1][2], rows[-1][3]), wps["A"]) if rows else None), gps_param=gps_param,
                   mode="B (video in the loop; precomputed estimates)", rtl=bool(o.get("rtl")))
    json.dump(summary, open(os.path.join(out, "summary.json"), "w"), indent=1)
    return summary


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--scenario", required=True); ap.add_argument("--estimates", required=True)
    ap.add_argument("--out", required=True); ap.add_argument("--seed", type=int, default=0)
    ap.add_argument("--speedup", type=int, default=8); ap.add_argument("--port", type=int, default=5760)
    ap.add_argument("--truth", default=os.path.join(os.path.dirname(__file__), "..", "data", "nidar", "truth.csv"))
    ap.add_argument("--mission", default=os.path.join(os.path.dirname(__file__), "..", "data", "nidar", "flight_sitl", "mission.json"))
    a = ap.parse_args()
    print(json.dumps(run(a.scenario, a.estimates, a.truth, a.mission, a.seed, a.out, speedup=a.speedup, port=a.port), indent=1))
