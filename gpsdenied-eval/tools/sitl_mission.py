"""Fly A->B->C->D at 200 m AGL in ArduCopter SITL and log true state + IMU + baro.
Output: <out>/flight.jsonl (one MAVLink message per line, with SITL boot time) and <out>/mission.json."""
import argparse, json, math, os, subprocess, sys, time
from pymavlink import mavutil

R = 6378137.0


def offset(lat, lon, east, north):
    return (lat + math.degrees(north / R), lon + math.degrees(east / (R * math.cos(math.radians(lat)))))


def dist_m(a, b):
    dn = math.radians(b[0] - a[0]) * R
    de = math.radians(b[1] - a[1]) * R * math.cos(math.radians(a[0]))
    return math.hypot(dn, de)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--ardupilot", default="/tmp/ardupilot")
    ap.add_argument("--out", required=True)
    ap.add_argument("--lat", type=float, default=18.5204); ap.add_argument("--lon", type=float, default=73.8567)
    ap.add_argument("--agl", type=float, default=200.0)
    ap.add_argument("--speed", type=float, default=12.0, help="cruise m/s")
    ap.add_argument("--speedup", type=int, default=4)
    ap.add_argument("--port", type=int, default=5760)
    a = ap.parse_args()
    os.makedirs(a.out, exist_ok=True)

    # A: south-west of map centre; legs: A->B 500 m N, B->C 500 m E, C->D 400 m S + 250 m W
    A = offset(a.lat, a.lon, -300, -300)
    B = offset(*A, 0, 500)
    C = offset(*B, 500, 0)
    D = offset(*C, -250, -400)
    wps = {"A": A, "B": B, "C": C, "D": D}
    json.dump({"waypoints": wps, "agl_m": a.agl, "cruise_mps": a.speed, "ardupilot": "Copter-4.5.7 SITL"},
              open(os.path.join(a.out, "mission.json"), "w"), indent=1)

    exe = os.path.join(a.ardupilot, "build/sitl/bin/arducopter")
    parm = os.path.join(a.ardupilot, "Tools/autotest/default_params/copter.parm")
    wd = os.path.join(a.out, "sitl_wd"); os.makedirs(wd, exist_ok=True)
    proc = subprocess.Popen([exe, "--model", "+", "--speedup", str(a.speedup), "--home", f"{A[0]},{A[1]},560,0",
                             "--defaults", parm, "-I0"], cwd=wd, stdout=open(os.path.join(wd, "sitl.log"), "w"),
                            stderr=subprocess.STDOUT)
    try:
        time.sleep(3)
        m = mavutil.mavlink_connection(f"tcp:127.0.0.1:{a.port}", retries=20) if False else None
        for _ in range(30):
            try:
                m = mavutil.mavlink_connection(f"tcp:127.0.0.1:{a.port}"); break
            except Exception:
                time.sleep(1)
        m.wait_heartbeat(timeout=30)
        print("heartbeat ok", flush=True)
        for k, v in {"ARMING_CHECK": 0, "WPNAV_SPEED": a.speed * 100, "WPNAV_ACCEL": 250, "GPS_TYPE": 1}.items():
            m.mav.param_set_send(m.target_system, m.target_component, k.encode(), v, mavutil.mavlink.MAV_PARAM_TYPE_REAL32)
            time.sleep(0.2)
        for mid, hz in [(33, 50), (30, 50), (27, 50), (29, 20), (164, 50), (32, 50)]:
            m.mav.command_long_send(m.target_system, m.target_component, 511, 0, mid, int(1e6 / hz), 0, 0, 0, 0, 0)
        log = open(os.path.join(a.out, "flight.jsonl"), "w")
        t_boot = [0.0]

        def pump(block=0.02):
            msg = m.recv_match(blocking=True, timeout=block)
            while msg is not None:
                d = msg.to_dict()
                if "time_boot_ms" in d: t_boot[0] = d["time_boot_ms"] / 1000.0
                d["_tboot"] = t_boot[0]
                if d["mavpackettype"] not in ("BAD_DATA",):
                    log.write(json.dumps(d) + "\n")
                msg = m.recv_match(blocking=False)

        def wait(cond, timeout, what):
            t0 = time.time()
            while time.time() - t0 < timeout:
                pump()
                if cond(): return
            raise RuntimeError("timeout: " + what)

        gp = {"lat": 0, "lon": 0, "rel": 0}

        def upd():
            g = m.messages.get("GLOBAL_POSITION_INT")
            if g: gp.update(lat=g.lat / 1e7, lon=g.lon / 1e7, rel=g.relative_alt / 1000.0)
            return g is not None

        wait(lambda: upd(), 30, "gpi")
        # wait for EKF origin / home then arm
        for _ in range(60):
            pump(0.1)
            sys_st = m.messages.get("SYS_STATUS")
        m.set_mode_apm("GUIDED") if hasattr(m, "set_mode_apm") else None
        mode_id = m.mode_mapping()["GUIDED"]
        m.mav.set_mode_send(m.target_system, mavutil.mavlink.MAV_MODE_FLAG_CUSTOM_MODE_ENABLED, mode_id)
        for attempt in range(40):
            m.mav.command_long_send(m.target_system, m.target_component, 400, 0, 1, 21196, 0, 0, 0, 0, 0)
            t0 = time.time()
            while time.time() - t0 < 1.5:
                pump()
                hb = m.messages.get("HEARTBEAT")
                if hb and hb.base_mode & 128: break
            if hb and hb.base_mode & 128: break
        else:
            raise RuntimeError("could not arm")
        print("armed", flush=True)
        m.mav.command_long_send(m.target_system, m.target_component, 22, 0, 0, 0, 0, 0, 0, 0, a.agl)
        wait(lambda: upd() and gp["rel"] > a.agl - 3, 400, "takeoff")
        print("at altitude", gp, flush=True)

        def goto(name, p):
            print("->", name, flush=True)
            mask = 0b0000111111111000
            m.mav.set_position_target_global_int_send(0, m.target_system, m.target_component, 6, mask,
                                                      int(p[0] * 1e7), int(p[1] * 1e7), a.agl, 0, 0, 0, 0, 0, 0, 0, 0)
            t0 = time.time()
            while time.time() - t0 < 600:
                pump(); upd()
                if dist_m((gp["lat"], gp["lon"]), p) < 4.0: return
                if int(time.time() - t0) % 5 == 0:
                    m.mav.set_position_target_global_int_send(0, m.target_system, m.target_component, 6, mask,
                                                              int(p[0] * 1e7), int(p[1] * 1e7), a.agl, 0, 0, 0, 0, 0, 0, 0, 0)
            raise RuntimeError("goto timeout " + name)

        mark = {}
        goto("A", A); mark["A"] = t_boot[0]
        for n in "BCD":
            goto(n, wps[n]); mark[n] = t_boot[0]
        for _ in range(100): pump(0.02)
        json.dump({"arrive_tboot": mark}, open(os.path.join(a.out, "arrivals.json"), "w"))
        log.close()
        print("done", mark, flush=True)
    finally:
        proc.terminate()


if __name__ == "__main__":
    main()
