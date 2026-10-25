"""Run loop: camera -> (telemetry hints) -> one worker thread per engine -> fusion -> recorder (+ optional GPS_INPUT).
Live: engines always take the newest frame and skip frames while busy. Replay with run.realtime=false: every frame goes to every engine."""
import logging, os, queue, signal, sys, threading, time
from .camera import open_camera
from .config import expand, normalize_paths
from .engines import EngineContext, make_engine, resolve_device
from .fusion import Fuser
from .geo import GeoMap
from .recorder import Recorder
from .telemetry import make_telemetry


class Runner:
    def __init__(self, cfg):
        self.cfg = normalize_paths(cfg); self.stop = threading.Event(); self.engines = cfg["run"]["engines"]
        self.rec = Recorder(cfg, self.engines); self.log = self._logger()
        self.rec.write_config(cfg)

    def _logger(self):
        log = logging.getLogger("nexnav"); log.handlers.clear(); log.setLevel(getattr(logging, self.cfg["run"]["log_level"].upper(), logging.INFO))
        fmt = logging.Formatter("%(asctime)s %(levelname)s %(message)s", "%H:%M:%S")
        for h in (logging.StreamHandler(sys.stdout), logging.FileHandler(os.path.join(self.rec.dir, "run.log"))): h.setFormatter(fmt); log.addHandler(h)
        return log

    def run(self):
        cfg = self.cfg; log = self.log; log.info("run folder: %s", self.rec.dir)
        signal.signal(signal.SIGINT, lambda *a: self.stop.set()); signal.signal(signal.SIGTERM, lambda *a: self.stop.set())
        geomap = GeoMap.load(expand(cfg["map"]["path"]), expand(cfg["map"]["georef"]))
        log.info("map %s: %dx%d px, %.3f m/px, centre %.5f,%.5f", geomap.name, geomap.width, geomap.height, geomap.ground_m_per_px(), *geomap.centre_latlon())
        realtime = cfg["run"]["realtime"]
        cam = open_camera(cfg["camera"], realtime=realtime)
        first = None
        for _ in range(50):
            first = cam.read(timeout=1.0)
            if first is not None: break
        if first is None: raise RuntimeError("camera delivered no frames")
        intr = cam.intr; log.info("camera %s: %dx%d f=%.1f px", cfg["camera"]["source"], intr.width, intr.height, intr.fx)
        self.tel = tel = make_telemetry(cfg); tel.start()
        device = resolve_device(cfg["engines"]["device"]); cache_dir = os.path.abspath(expand(cfg["map"]["cache_dir"])); os.makedirs(cache_dir, exist_ok=True)
        ctx = EngineContext(cfg, geomap, intr, device, log, cache_dir)
        cfg["_gps_probe"] = lambda t: ((lambda g: (g.lat, g.lon) if g else (float("nan"), float("nan")))(tel.gps_at(t)))
        eng = {}
        for name in self.engines:
            e = make_engine(name); t0 = time.time(); e.load(ctx); log.info("engine %s ready in %.1fs on %s %s", name, time.time() - t0, device, getattr(e, "init_info", {})); eng[name] = e
        self.rec.engine_init = {n: getattr(e, "init_info", {}) for n, e in eng.items()}
        fuser = Fuser(cfg, self.engines) if len(self.engines) > 1 else Fuser(cfg, self.engines)
        sender = None
        if cfg["output"]["mavlink_gps_input"]["enabled"]:
            from .mavout import GpsInputSender
            sender = GpsInputSender(cfg, tel, log); log.warning("GPS_INPUT injection is ENABLED")
        lock = threading.Lock(); latest_hints = {}
        qs = {n: queue.Queue(maxsize=1) for n in eng}

        def gps_for(t):
            g = tel.gps_at(t)
            if g is None: return None
            return (g, tel.gps_age(g, t))

        done = threading.Event()

        def worker(name):
            e = eng[name]
            while True:
                try: item = qs[name].get(timeout=0.5)
                except queue.Empty:
                    if done.is_set(): return
                    continue
                frame, hints = item
                try:
                    fix = e.process(frame, hints)
                except Exception as ex:
                    log.exception("engine %s failed on frame %d: %s", name, frame.index, ex); continue
                fix.info["vn"], fix.info["ve"] = hints.vn, hints.ve        # lets the fuser carry this fix to a later time
                gps = gps_for(frame.t)
                self.rec.estimate(fix, gps)
                fz = fuser.update(fix)
                if len(self.engines) > 1: self.rec.fused(fz, gps_for(fz.t))
                else: pass
                if sender is not None:
                    try: sender.send(fz, hints)
                    except Exception as ex: log.warning("GPS_INPUT send failed: %s", ex)
                log.debug("%s t=%.2f acc=%s lat=%.6f lon=%.6f %.0f ms", name, frame.t, fix.accepted, fix.lat, fix.lon, fix.latency_s * 1000)

        threads = [threading.Thread(target=worker, args=(n,), daemon=True) for n in eng]
        for th in threads: th.start()
        t_start = time.monotonic(); next_print = t_start + cfg["run"]["progress_every_s"]; maxs = cfg["run"]["max_seconds"]; amin, amax = cfg["telemetry"]["altitude"]["min_m"], cfg["telemetry"]["altitude"]["max_m"]
        save_every = cfg["output"]["save_frames_every"]; frames_dir = None
        if save_every:
            import cv2
            frames_dir = os.path.join(self.rec.dir, "frames"); os.makedirs(frames_dir, exist_ok=True)
        frame = first; t_first = first.t
        try:
            while not self.stop.is_set():
                if frame is not None:
                    hints = tel.at(frame.t)
                    if hints.ok and not (amin <= hints.alt_agl <= amax): hints.ok = False; hints.reason = f"altitude {hints.alt_agl:.0f} m outside [{amin:.0f},{amax:.0f}]"
                    offered = []
                    if hints.ok:
                        for n in eng:
                            if realtime:
                                try: qs[n].get_nowait()           # drop the stale pending frame: newest wins
                                except queue.Empty: pass
                                qs[n].put((frame, hints))
                            else:
                                while not self.stop.is_set():
                                    try: qs[n].put((frame, hints), timeout=0.5); break
                                    except queue.Full: pass
                            offered.append(n)
                    self.rec.frame(frame, hints, offered, gps_for(frame.t))
                    if frames_dir and frame.index % save_every == 0:
                        import cv2
                        cv2.imwrite(os.path.join(frames_dir, f"{frame.t:010.3f}.jpg"), cv2.resize(frame.image, None, fx=cfg["output"]["save_frame_scale"], fy=cfg["output"]["save_frame_scale"]))
                now = time.monotonic()
                if now >= next_print:
                    next_print = now + cfg["run"]["progress_every_s"]
                    log.info("t+%.0fs frames=%d | %s", now - t_start, self.rec.frames, " | ".join(f"{n}: {len(self.rec.rows[n])} fixes, {sum(r['accepted'] for r in self.rec.rows[n])} ok" for n in eng))
                if maxs and (now - t_start) >= maxs: log.info("max_seconds reached"); break
                frame = cam.read(timeout=1.0)
                if frame is None:
                    if not cam.live: log.info("end of replay input"); break
                    continue
        finally:
            done.set()
            for th in threads: th.join(timeout=600 if not realtime else 60)
            for n in eng: eng[n].close()
            cam.close(); tel.stop()
            if hasattr(tel, "log_rows") and tel.log_rows():
                import csv
                rows = tel.log_rows()
                with open(os.path.join(self.rec.dir, "telemetry.csv"), "w", newline="") as f:
                    w = csv.DictWriter(f, list(rows[0].keys())); w.writeheader(); w.writerows(rows)
            summary = self.rec.finish(dict(engine_init=getattr(self.rec, "engine_init", {}), gps_input_sent=getattr(sender, "n", 0) if sender else 0))
            log.info("summary: %s", os.path.join(self.rec.dir, "summary.md"))
        return summary
