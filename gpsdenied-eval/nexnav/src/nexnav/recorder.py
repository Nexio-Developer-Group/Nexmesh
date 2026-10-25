"""One folder per run with everything needed to audit it:
  config.resolved.yaml, system.json, run.log
  estimates_<engine>.csv   per processed frame: estimate, confidence, latency + comparison with the real GPS (error and the correction that would map the estimate onto the GPS)
  fused.csv                the redundancy output with the same comparison columns
  frames.csv               per camera frame: telemetry hints, which engines processed it, GPS reference at that time
  telemetry.csv            raw MAVLink telemetry log (live runs)
  summary.json / summary.md  per-engine and fused statistics
Correction columns: corr_north_m / corr_east_m = (GPS - estimate) in metres, corr_lat_deg / corr_lon_deg likewise in degrees."""
import csv, datetime, json, math, os, platform, socket, threading, time
import numpy as np
from .geo import enu_delta_m, haversine_m

EST_COLS = ["t", "wall_utc", "engine", "accepted", "lat", "lon", "alt_est_m", "yaw_est_deg", "conf", "latency_ms", "raw_lat", "raw_lon", "info",
            "gps_lat", "gps_lon", "gps_fix", "gps_age_s", "err_north_m", "err_east_m", "err_m", "corr_north_m", "corr_east_m", "corr_lat_deg", "corr_lon_deg"]
FUSED_COLS = ["t", "wall_utc", "accepted", "lat", "lon", "conf", "source", "quality", "disagree_m",
              "gps_lat", "gps_lon", "gps_fix", "gps_age_s", "err_north_m", "err_east_m", "err_m", "corr_north_m", "corr_east_m", "corr_lat_deg", "corr_lon_deg"]
FRAME_COLS = ["t", "index", "alt_agl_m", "roll_deg", "pitch_deg", "yaw_deg", "vn", "ve", "vu", "hints_ok", "reason", "offered_to", "gps_lat", "gps_lon", "gps_fix"]


def _nan(x): return x is None or (isinstance(x, float) and math.isnan(x))


class Recorder:
    def __init__(self, cfg, engines):
        o = cfg["run"]; base = os.path.abspath(os.path.expanduser(o["output_dir"])); os.makedirs(base, exist_ok=True)
        stamp = datetime.datetime.now().strftime("%Y%m%d-%H%M%S"); name = f"{stamp}_{o['name']}"; d = os.path.join(base, name); k = 1
        while os.path.exists(d): k += 1; d = os.path.join(base, f"{name}-{k}")
        os.makedirs(d); self.dir = d; self.cfg = cfg; self.engines = engines; self.lock = threading.Lock(); self.t0 = time.time()
        self.files = {}; self.writers = {}
        for e in engines: self._open(f"estimates_{e}.csv", EST_COLS)
        if len(engines) > 1: self._open("fused.csv", FUSED_COLS)
        self._open("frames.csv", FRAME_COLS)
        self.rows = {e: [] for e in engines}; self.rows["fused"] = []; self.frames = 0; self.offered = {e: 0 for e in engines}
        self.skipped = {}; self.fused_quality = {}

    def _open(self, name, cols):
        f = open(os.path.join(self.dir, name), "w", newline=""); w = csv.DictWriter(f, cols); w.writeheader(); self.files[name] = f; self.writers[name] = w

    def write_config(self, cfg):
        import yaml
        clean = {k: v for k, v in cfg.items() if not k.startswith("_")}
        yaml.safe_dump(clean, open(os.path.join(self.dir, "config.resolved.yaml"), "w"), sort_keys=False)
        info = dict(host=socket.gethostname(), platform=platform.platform(), python=platform.python_version(), started_utc=datetime.datetime.now(datetime.timezone.utc).isoformat())
        try:
            import torch; info.update(torch=torch.__version__, cuda=torch.cuda.is_available(), gpu=torch.cuda.get_device_name(0) if torch.cuda.is_available() else None)
        except Exception: pass
        try:
            import cv2; info["opencv"] = cv2.__version__
        except Exception: pass
        try:
            from . import __version__; info["nexnav"] = __version__
        except Exception: pass
        try: info["jetson"] = open("/etc/nv_tegra_release").read().strip()
        except Exception: info["jetson"] = None
        json.dump(info, open(os.path.join(self.dir, "system.json"), "w"), indent=1)

    @staticmethod
    def _compare(lat, lon, gps):
        out = dict(gps_lat="", gps_lon="", gps_fix="", gps_age_s="", err_north_m="", err_east_m="", err_m="", corr_north_m="", corr_east_m="", corr_lat_deg="", corr_lon_deg="")
        if gps is None: return out
        g, age = gps
        out.update(gps_lat=f"{g.lat:.8f}", gps_lon=f"{g.lon:.8f}", gps_fix=g.fix_type, gps_age_s=f"{age:.2f}")
        if _nan(lat) or _nan(lon) or g.fix_type < 3: return out
        n, e = enu_delta_m(g.lat, g.lon, lat, lon)      # estimate relative to GPS
        out.update(err_north_m=f"{float(n):.3f}", err_east_m=f"{float(e):.3f}", err_m=f"{math.hypot(float(n), float(e)):.3f}",
                   corr_north_m=f"{-float(n):.3f}", corr_east_m=f"{-float(e):.3f}", corr_lat_deg=f"{g.lat - lat:.8f}", corr_lon_deg=f"{g.lon - lon:.8f}")
        return out

    def frame(self, frame, hints, offered, gps):
        with self.lock:
            self.frames += 1
            for e in offered: self.offered[e] += 1
            if not hints.ok: self.skipped[hints.reason] = self.skipped.get(hints.reason, 0) + 1
            g = gps[0] if gps else None
            self.writers["frames.csv"].writerow(dict(t=f"{frame.t:.3f}", index=frame.index, alt_agl_m=f"{hints.alt_agl:.2f}", roll_deg=f"{math.degrees(hints.roll):.2f}",
                pitch_deg=f"{math.degrees(hints.pitch):.2f}", yaw_deg=f"{math.degrees(hints.yaw):.2f}", vn=f"{hints.vn:.2f}", ve=f"{hints.ve:.2f}", vu=f"{hints.vu:.2f}",
                hints_ok=int(hints.ok), reason=hints.reason, offered_to="+".join(offered), gps_lat=f"{g.lat:.8f}" if g else "", gps_lon=f"{g.lon:.8f}" if g else "", gps_fix=g.fix_type if g else ""))
            if self.frames % 20 == 0: self.files["frames.csv"].flush()

    def estimate(self, fix, gps):
        with self.lock:
            cmp_ = self._compare(fix.lat if fix.accepted else float("nan"), fix.lon if fix.accepted else float("nan"), gps)
            row = dict(t=f"{fix.t:.3f}", wall_utc=datetime.datetime.now(datetime.timezone.utc).strftime("%H:%M:%S.%f")[:-3], engine=fix.engine, accepted=int(fix.accepted),
                       lat=f"{fix.lat:.8f}" if fix.accepted else "", lon=f"{fix.lon:.8f}" if fix.accepted else "", alt_est_m="" if _nan(fix.alt) else f"{fix.alt:.1f}",
                       yaw_est_deg="" if _nan(fix.yaw) else f"{fix.yaw:.1f}", conf=f"{fix.conf:.3f}", latency_ms=f"{fix.latency_s * 1000:.1f}",
                       raw_lat="" if _nan(fix.raw_lat) else f"{fix.raw_lat:.8f}", raw_lon="" if _nan(fix.raw_lon) else f"{fix.raw_lon:.8f}",
                       info=json.dumps({k: (round(v, 4) if isinstance(v, float) else v) for k, v in fix.info.items()}, default=str), **cmp_)
            self.writers[f"estimates_{fix.engine}.csv"].writerow(row); self.files[f"estimates_{fix.engine}.csv"].flush()
            self.rows[fix.engine].append(dict(t=fix.t, accepted=fix.accepted, err=float(cmp_["err_m"]) if cmp_["err_m"] != "" else None,
                                              cn=float(cmp_["corr_north_m"]) if cmp_["corr_north_m"] != "" else None, ce=float(cmp_["corr_east_m"]) if cmp_["corr_east_m"] != "" else None,
                                              lat_ms=fix.latency_s * 1000, conf=fix.conf))

    def fused(self, fz, gps):
        if "fused.csv" not in self.writers: return
        with self.lock:
            cmp_ = self._compare(fz.lat if fz.accepted else float("nan"), fz.lon if fz.accepted else float("nan"), gps)
            row = dict(t=f"{fz.t:.3f}", wall_utc=datetime.datetime.now(datetime.timezone.utc).strftime("%H:%M:%S.%f")[:-3], accepted=int(fz.accepted), lat=f"{fz.lat:.8f}" if fz.accepted else "",
                       lon=f"{fz.lon:.8f}" if fz.accepted else "", conf=f"{fz.conf:.3f}", source=fz.source, quality=fz.quality,
                       disagree_m="" if _nan(fz.disagree_m) else f"{fz.disagree_m:.1f}", **cmp_)
            self.writers["fused.csv"].writerow(row); self.files["fused.csv"].flush()
            self.fused_quality[fz.quality] = self.fused_quality.get(fz.quality, 0) + 1
            self.rows["fused"].append(dict(t=fz.t, accepted=fz.accepted, err=float(cmp_["err_m"]) if cmp_["err_m"] != "" else None,
                                           cn=float(cmp_["corr_north_m"]) if cmp_["corr_north_m"] != "" else None, ce=float(cmp_["corr_east_m"]) if cmp_["corr_east_m"] != "" else None,
                                           lat_ms=0.0, conf=fz.conf))

    @staticmethod
    def _stats(rows):
        n = len(rows); acc = [r for r in rows if r["accepted"]]; errs = np.array([r["err"] for r in acc if r["err"] is not None])
        s = dict(processed=n, accepted=len(acc), fix_rate_pct=round(100.0 * len(acc) / n, 1) if n else None, compared=int(len(errs)))
        if len(errs):
            s.update(err_median_m=round(float(np.median(errs)), 2), err_p95_m=round(float(np.percentile(errs, 95)), 2), err_rmse_m=round(float(np.sqrt(np.mean(errs ** 2))), 2),
                     err_max_m=round(float(errs.max()), 2), within_10m_pct=round(100.0 * float(np.mean(errs <= 10)), 1), within_50m_pct=round(100.0 * float(np.mean(errs <= 50)), 1),
                     within_100m_pct=round(100.0 * float(np.mean(errs <= 100)), 1))
            cn = np.array([r["cn"] for r in acc if r["cn"] is not None]); ce = np.array([r["ce"] for r in acc if r["ce"] is not None])
            s.update(mean_correction_north_m=round(float(cn.mean()), 2), mean_correction_east_m=round(float(ce.mean()), 2))
        lat = np.array([r["lat_ms"] for r in rows]) if rows else np.array([])
        if lat.size and lat.max() > 0: s.update(latency_p50_ms=round(float(np.percentile(lat, 50)), 1), latency_p95_ms=round(float(np.percentile(lat, 95)), 1))
        return s

    def finish(self, extra=None):
        with self.lock:
            for f in self.files.values(): f.flush(); f.close()
            summary = dict(run_dir=self.dir, duration_s=round(time.time() - self.t0, 1), frames_seen=self.frames, frames_skipped=self.skipped, offered=self.offered,
                           note="errors are measured against the real GPS reference, valid only while that reference is trustworthy")
            summary["engines"] = {e: self._stats(self.rows[e]) for e in self.engines}
            for e in self.engines: summary["engines"][e]["dropped_busy"] = max(0, self.offered[e] - len(self.rows[e]))
            if len(self.engines) > 1: summary["fused"] = self._stats(self.rows["fused"]); summary["fused"]["quality_counts"] = self.fused_quality
            if extra: summary.update(extra)
            json.dump(summary, open(os.path.join(self.dir, "summary.json"), "w"), indent=1)
            lines = [f"# Run summary: {os.path.basename(self.dir)}", "", f"Duration {summary['duration_s']} s, {self.frames} camera frames seen.", "",
                     "| output | processed | accepted | fix rate % | compared with GPS | median err m | p95 m | max m | mean correction N/E m | latency p50/p95 ms |", "|---|---|---|---|---|---|---|---|---|---|"]
            items = [(e, summary["engines"][e]) for e in self.engines] + ([("fused", summary["fused"])] if "fused" in summary else [])
            for n, s in items:
                lines.append(f"| {n} | {s['processed']} | {s['accepted']} | {s['fix_rate_pct']} | {s['compared']} | {s.get('err_median_m', '-')} | {s.get('err_p95_m', '-')} | {s.get('err_max_m', '-')} | "
                             f"{s.get('mean_correction_north_m', '-')}/{s.get('mean_correction_east_m', '-')} | {s.get('latency_p50_ms', '-')}/{s.get('latency_p95_ms', '-')} |")
            open(os.path.join(self.dir, "summary.md"), "w").write("\n".join(lines) + "\n")
            return summary
