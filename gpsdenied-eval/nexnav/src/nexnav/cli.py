"""Command line: nexnav run | check | setup | config-template | config-docs | map-info | map-fetch | summarize"""
import argparse, json, os, subprocess, sys


def _overrides(a):
    o = {}
    if getattr(a, "engines", None): o["run.engines"] = ["s6", "s5"] if a.engines == "both" else [e for e in a.engines.split(",") if e]
    for attr, key in (("camera", "camera.source"), ("map", "map.path"), ("mavlink", "telemetry.mavlink.connection"), ("max_seconds", "run.max_seconds"),
                      ("name", "run.name"), ("output_dir", "run.output_dir"), ("device", "engines.device")):
        v = getattr(a, attr, None)
        if v is not None: o[key] = v
    if getattr(a, "telemetry_csv", None): o["telemetry.source"] = "csv"; o["telemetry.csv.path"] = a.telemetry_csv
    if getattr(a, "no_realtime", False): o["run.realtime"] = False
    for kv in getattr(a, "set", None) or []:
        k, v = kv.split("=", 1)
        try: v = json.loads(v)
        except Exception: pass
        o[k] = v
    return o


def _load(a):
    from .config import load_config
    cfg = load_config(a.config, _overrides(a))
    if cfg["engines"]["device"] == "cpu": os.environ["CUDA_VISIBLE_DEVICES"] = "-1"   # must be set before torch is imported
    return cfg


def cmd_run(a):
    cfg = _load(a)
    from .runner import Runner
    s = Runner(cfg).run()
    print(open(os.path.join(s["run_dir"], "summary.md")).read())


def cmd_check(a):
    cfg = _load(a); ok = True
    def rep(name, good, msg=""):
        nonlocal ok; ok &= bool(good); print(f"[{'OK' if good else 'FAIL'}] {name} {msg}")
    from .config import expand
    from .geo import GeoMap
    try:
        gm = GeoMap.load(expand(cfg["map"]["path"]), expand(cfg["map"]["georef"])); rep("map", True, f"{gm.width}x{gm.height}px {gm.ground_m_per_px():.3f} m/px centre {gm.centre_latlon()}")
    except Exception as e: rep("map", False, repr(e))
    try:
        import torch; rep("torch", True, f"{torch.__version__} cuda={torch.cuda.is_available()}" + (f" ({torch.cuda.get_device_name(0)})" if torch.cuda.is_available() else ""))
    except Exception as e: rep("torch", False, repr(e))
    tp = expand(cfg["engines"]["third_party_dir"])
    if "s6" in cfg["run"]["engines"]:
        for sub in ("MegaLoc", "accelerated_features"): rep(f"S6 dependency {sub}", os.path.isdir(os.path.join(tp, sub)), f"({os.path.join(tp, sub)})")
    if "s5" in cfg["run"]["engines"]:
        r = expand(cfg["engines"]["s5"]["repo_path"]); rep("S5 repository", bool(r) and os.path.isfile(os.path.join(r, "PFtool.py")), f"({r})")
    try:
        from .camera import open_camera
        cam = open_camera(cfg["camera"], realtime=False); fr = None
        for _ in range(30):
            fr = cam.read(timeout=1.0)
            if fr is not None: break
        rep("camera", fr is not None, f"{cfg['camera']['source']} -> {fr.image.shape if fr is not None else 'no frame'}; intrinsics {cam.intr.as_dict() if cam.intr else None}"); cam.close()
    except Exception as e: rep("camera", False, repr(e))
    try:
        from .telemetry import make_telemetry
        import time
        t = make_telemetry(cfg); t.start(); time.sleep(2.0); h = t.at(time.monotonic()); rep("telemetry", h.ok, f"alt={h.alt_agl:.1f} m yaw={__import__('math').degrees(h.yaw):.0f} deg {h.reason}"); t.stop()
    except Exception as e: rep("telemetry", False, repr(e))
    print("READY" if ok else "NOT READY"); sys.exit(0 if ok else 1)


def cmd_setup(a):
    tp = os.path.expanduser(a.dir or "~/.nexnav/third_party"); os.makedirs(tp, exist_ok=True)
    repos = {"s6": [("MegaLoc", "https://github.com/gmberton/MegaLoc", "MIT"), ("accelerated_features", "https://github.com/verlab/accelerated_features", "Apache-2.0 (LighterGlue weights licence unconfirmed)")],
             "s5": [("SWA-PF", "https://github.com/YuanJiayuuu/SWA-PF", "NO LICENCE DECLARED: evaluation use only, never redistributed")]}
    want = [k for k in ("s6", "s5") if getattr(a, k) or not (a.s6 or a.s5)]
    for k in want:
        for name, url, lic in repos[k]:
            d = os.path.join(tp, name)
            if os.path.isdir(d): print(f"= {name}: already at {d}"); continue
            print(f"+ cloning {url} ({lic})"); subprocess.check_call(["git", "clone", "--depth", "1", url, d])
    if "s5" in want: print(f"\nSet in your config:  engines.s5.repo_path: {os.path.join(tp, 'SWA-PF')}")
    if a.prefetch and "s6" in want:
        import torch
        torch.hub.load(os.path.join(tp, "MegaLoc"), "get_trained_model", source="local"); print("MegaLoc weights cached")
    print(f"\nthird_party_dir = {tp}")


def cmd_template(a):
    from .config import template_yaml
    txt = template_yaml()
    if a.output: open(a.output, "w").write(txt); print("wrote", a.output)
    else: print(txt)


def cmd_docs(a):
    from .config import config_markdown
    print(config_markdown())


def cmd_mapinfo(a):
    from .geo import GeoMap
    gm = GeoMap.load(a.path, a.georef); print(f"{gm.width}x{gm.height} px, {gm.ground_m_per_px():.3f} m/px, centre {gm.centre_latlon()}")
    for n, (x, y) in {"top-left": (0, 0), "bottom-right": (gm.width, gm.height)}.items(): print(n, gm.px_to_latlon(x, y))


def cmd_mapfetch(a):
    from .tools.mapfetch import fetch
    fetch(a.url_template, a.lat, a.lon, a.half_m, a.zoom, a.out)


def cmd_summarize(a):
    import csv, glob
    from .recorder import Recorder
    d = a.run_dir; out = {}
    for f in sorted(glob.glob(os.path.join(d, "estimates_*.csv")) + glob.glob(os.path.join(d, "fused.csv"))):
        rows = []
        for r in csv.DictReader(open(f)):
            rows.append(dict(t=float(r["t"]), accepted=r["accepted"] == "1", err=float(r["err_m"]) if r["err_m"] else None, cn=float(r["corr_north_m"]) if r["corr_north_m"] else None,
                             ce=float(r["corr_east_m"]) if r["corr_east_m"] else None, lat_ms=float(r.get("latency_ms") or 0), conf=0))
        out[os.path.basename(f)[:-4]] = Recorder._stats(rows)
    print(json.dumps(out, indent=1))


def main(argv=None):
    p = argparse.ArgumentParser(prog="nexnav", description="GPS-denied absolute positioning (S6 / S5 engines)")
    sub = p.add_subparsers(dest="cmd", required=True)
    def common(sp):
        sp.add_argument("-c", "--config"); sp.add_argument("--engines", help="s6 | s5 | both (or s6,s5)"); sp.add_argument("--camera"); sp.add_argument("--map")
        sp.add_argument("--telemetry-csv"); sp.add_argument("--mavlink"); sp.add_argument("--max-seconds", type=float); sp.add_argument("--name"); sp.add_argument("--output-dir")
        sp.add_argument("--device", choices=["auto", "cuda", "cpu"]); sp.add_argument("--no-realtime", action="store_true", help="replay: process every frame"); sp.add_argument("--set", action="append", metavar="KEY=VALUE")
    r = sub.add_parser("run", help="run the localiser"); common(r); r.set_defaults(f=cmd_run)
    c = sub.add_parser("check", help="verify map, camera, telemetry, dependencies"); common(c); c.set_defaults(f=cmd_check)
    s = sub.add_parser("setup", help="clone MegaLoc, XFeat, SWA-PF"); s.add_argument("--s6", action="store_true"); s.add_argument("--s5", action="store_true"); s.add_argument("--dir"); s.add_argument("--prefetch", action="store_true"); s.set_defaults(f=cmd_setup)
    t = sub.add_parser("config-template", help="write a commented config"); t.add_argument("-o", "--output"); t.set_defaults(f=cmd_template)
    d = sub.add_parser("config-docs", help="print the parameter table (markdown)"); d.set_defaults(f=cmd_docs)
    m = sub.add_parser("map-info"); m.add_argument("path"); m.add_argument("--georef"); m.set_defaults(f=cmd_mapinfo)
    f = sub.add_parser("map-fetch", help="build a map mosaic from an XYZ tile server YOU are licensed to use")
    f.add_argument("--url-template", required=True, help="e.g. https://tiles.example.com/{z}/{x}/{y}.jpg"); f.add_argument("--lat", type=float, required=True); f.add_argument("--lon", type=float, required=True)
    f.add_argument("--half-m", type=float, default=900); f.add_argument("--zoom", type=int, default=19); f.add_argument("--out", required=True); f.set_defaults(f=cmd_mapfetch)
    z = sub.add_parser("summarize", help="recompute statistics of a run folder"); z.add_argument("run_dir"); z.set_defaults(f=cmd_summarize)
    a = p.parse_args(argv); a.f(a)
