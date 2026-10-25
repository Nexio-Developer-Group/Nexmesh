"""Configuration: every parameter lives in SCHEMA with its default and a one-line description.
`load_config` merges a YAML/JSON file over the defaults and rejects unknown keys (typos); `template_yaml` writes a fully
commented config you can edit; `docs/CONFIG.md` is generated from the same table."""
import copy, json, os

# (dotted key, default, description)
SCHEMA = [
 # ---------------------------------------------------------------- run
 ("run.name", "flight", "Label appended to the run folder name (runs/<timestamp>_<name>/)."),
 ("run.output_dir", "runs", "Folder that receives one sub-folder per run."),
 ("run.engines", ["s6"], "Engines to run: [s6], [s5] or [s6, s5] (both together for redundancy)."),
 ("run.max_seconds", 0, "Stop after this many seconds of (wall or replay) time; 0 = until the camera ends or Ctrl-C."),
 ("run.realtime", True, "True: engines always work on the newest frame and skip frames while busy (live behaviour). False (replay only): process every frame, as fast as possible."),
 ("run.log_level", "INFO", "DEBUG, INFO, WARNING."),
 ("run.seed", 0, "Random seed (particle filter, RANSAC fallback)."),
 ("run.progress_every_s", 5.0, "Print a status line every N seconds."),
 # ---------------------------------------------------------------- camera
 ("camera.source", "v4l2:0", "v4l2:<index or /dev/videoN> | csi:<sensor-id> (Jetson CSI via nvarguscamerasrc) | gst:<full GStreamer pipeline ending in appsink> | rtsp://... | file:<video> | folder:<dir of images>."),
 ("camera.width", 1280, "Requested capture width in pixels (ignored for file/folder sources)."),
 ("camera.height", 800, "Requested capture height in pixels."),
 ("camera.fps", 30.0, "Requested capture frame rate."),
 ("camera.fx", None, "Focal length x in pixels (omit and give hfov_deg instead if unknown)."),
 ("camera.fy", None, "Focal length y in pixels (default = fx)."),
 ("camera.cx", None, "Principal point x (default = width/2)."),
 ("camera.cy", None, "Principal point y (default = height/2)."),
 ("camera.hfov_deg", None, "Horizontal field of view in degrees; used to derive fx when fx is not given."),
 ("camera.distortion", None, "OpenCV distortion [k1, k2, p1, p2, (k3)] or null. Frames are undistorted before the engines see them."),
 ("camera.mount", "nadir_stabilised", "nadir_stabilised: gimbal keeps the camera pointing straight down (roll/pitch ignored). body_fixed: camera is rigid on the airframe, so vehicle roll/pitch are used to rectify the image."),
 ("camera.mount_roll_deg", 0.0, "body_fixed only: camera roll offset relative to the vehicle (deg)."),
 ("camera.mount_pitch_deg", 0.0, "body_fixed only: camera pitch offset relative to a straight-down view (deg)."),
 ("camera.mount_yaw_deg", 0.0, "Rotation of the image 'up' direction relative to the vehicle nose, clockwise (deg)."),
 ("camera.flip", "none", "none | h | v | 180: fix a mirrored or upside-down camera."),
 ("camera.replay.timing", "recorded", "folder/file sources only. recorded: take frame times from file names (<seconds>.png) or the video clock; fps: use replay.fps."),
 ("camera.replay.fps", 5.0, "Frame rate used when replay.timing = fps (folder sources)."),
 ("camera.replay.start_s", 0.0, "Skip frames before this time (replay sources)."),
 ("camera.replay.stride", 1, "Use every Nth frame (replay sources)."),
 ("camera.timestamp_offset_s", 0.0, "Constant camera-to-telemetry delay (s); positive = camera frames are older than their timestamp says."),
 # ---------------------------------------------------------------- map
 ("map.path", None, "Reference map: GeoTIFF, or PNG/JPG with a JSON georeference sidecar (see docs/MAPS.md)."),
 ("map.georef", None, "Sidecar JSON path when it is not <map>.json."),
 ("map.cache_dir", "~/.nexnav/cache", "Where tile descriptors / semantic labels are cached (rebuilt only when the map or settings change)."),
 ("map.work_gsd_m", 0.283, "Both engines work at this ground resolution (metres/pixel); the map is resampled once on load. 0 = keep the native resolution."),
 ("map.roi", None, "Optional [lat_min, lon_min, lat_max, lon_max] box to restrict the map (saves index time)."),
 # ---------------------------------------------------------------- telemetry
 ("telemetry.source", "mavlink", "mavlink | csv (replay a telemetry file) | fixed (constant values, for bench tests)."),
 ("telemetry.mavlink.connection", "udpin:0.0.0.0:14550", "pymavlink connection string, e.g. serial:/dev/ttyTHS1:921600, udpin:0.0.0.0:14550, tcp:127.0.0.1:5760."),
 ("telemetry.mavlink.source_system", 245, "MAVLink system id this companion computer uses."),
 ("telemetry.mavlink.source_component", 191, "MAVLink component id."),
 ("telemetry.mavlink.stream_rate_hz", 10, "Rate requested for ATTITUDE / GLOBAL_POSITION_INT / GPS_RAW_INT (via SET_MESSAGE_INTERVAL)."),
 ("telemetry.mavlink.heartbeat_timeout_s", 15.0, "Give up waiting for the autopilot heartbeat after this long."),
 ("telemetry.csv.path", None, "CSV with columns t, lat, lon, alt, yaw (deg) and optionally roll, pitch (deg), vn, ve, vu (m/s)."),
 ("telemetry.fixed.alt_agl_m", 200.0, "fixed source: height above ground (m)."),
 ("telemetry.fixed.yaw_deg", 0.0, "fixed source: heading (deg clockwise from north)."),
 ("telemetry.fixed.lat", None, "fixed source: GPS reference latitude (optional)."),
 ("telemetry.fixed.lon", None, "fixed source: GPS reference longitude (optional)."),
 ("telemetry.altitude.source", "relative_alt", "Height above ground used to scale the image: relative_alt (MAVLink GLOBAL_POSITION_INT.relative_alt, i.e. above home) | rangefinder (DISTANCE_SENSOR) | vfr_hud (VFR_HUD.alt, MSL) | csv | fixed."),
 ("telemetry.altitude.fixed_m", 200.0, "Height used when altitude.source = fixed."),
 ("telemetry.altitude.offset_m", 0.0, "Added to the altitude source: terrain elevation of the map area minus home elevation (m)."),
 ("telemetry.altitude.min_m", 30.0, "Frames below this height are skipped."),
 ("telemetry.altitude.max_m", 800.0, "Frames above this height are skipped."),
 ("telemetry.gps_reference", "gps_raw", "Real GPS used ONLY for comparison logs: gps_raw (GPS_RAW_INT) | global_position (EKF position) | none."),
 ("telemetry.gps_min_fix_type", 3, "Reference GPS samples with a lower fix type are not compared."),
 ("telemetry.gps_max_age_s", 1.0, "Reference GPS samples older than this at the time of an estimate are not compared."),
 ("telemetry.max_age_s", 2.0, "Attitude / altitude samples older than this at frame time make the frame unusable."),
 # ---------------------------------------------------------------- engines
 ("engines.device", "auto", "auto | cuda | cpu (both engines)."),
 ("engines.threads", 0, "CPU threads for torch/OpenCV; 0 = leave the default."),
 ("engines.third_party_dir", "~/.nexnav/third_party", "Where `nexnav setup` clones MegaLoc, XFeat and the SWA-PF repository."),
 ("engines.hf_home", None, "HF_HOME override (model weights cache)."),
 ("engines.torch_home", None, "TORCH_HOME override."),
 # --- S6
 ("engines.s6.retriever", "megaloc", "megaloc | dinov2s (place-recognition network for the coarse search)."),
 ("engines.s6.matcher", "xfeat_lg", "xfeat_lg (XFeat + LighterGlue, fast) | xfeat_mnn | splg (SuperPoint + LightGlue: non-commercial weights)."),
 ("engines.s6.fp16", False, "CUDA only: run the retriever under half precision autocast (untested on a Jetson here)."),
 ("engines.s6.tile_scales_m", [145.0, 290.0], "Tile sizes (m) of the descriptor indices. A query uses the scale closest to its image footprint, so cover your altitude range: footprint short side ~ 0.72 x altitude (60 deg HFOV, 16:10). 75 m suits ~100 m, 145 m ~200 m, 290 m ~400 m, 580 m ~800 m."),
 ("engines.s6.stride_frac", 0.5, "Tile stride as a fraction of the tile size."),
 ("engines.s6.q_crop_frac", 0.76, "Central crop of the north-up query used for retrieval, as a fraction of the footprint's short side (author-tuned value gives a 109 m crop at 200 m)."),
 ("engines.s6.ret_scale", 0.5, "Retrieval image scale (map px -> network px)."),
 ("engines.s6.topk", 5, "Tiles verified per query."),
 ("engines.s6.early_inl", 70, "Stop verifying further tiles at this many inliers."),
 ("engines.s6.nu_size", "auto", "Side (px at work GSD) of the north-up rectified canvas: auto = 1.1 x footprint diagonal, at least 1024 and at most max_canvas_px; or an integer."),
 ("engines.s6.max_canvas_px", 2304, "Upper limit of the auto canvas."),
 ("engines.s6.ref_size_m", 362.0, "Side (m) of the map patch matched against (grows automatically with the footprint)."),
 ("engines.s6.kpts", 2048, "Keypoints per image."),
 ("engines.s6.min_inl", 20, "Minimum PnP inliers for a fix."),
 ("engines.s6.ransac_px", 4.0, "RANSAC reprojection threshold (px in the rectified canvas)."),
 ("engines.s6.gate", True, "Consistency gate on/off."),
 ("engines.s6.gate_h_frac", 0.10, "Gate: allowed |PnP height - altitude source| as a fraction of the height ..."),
 ("engines.s6.gate_h_min_m", 12.0, "... but at least this many metres."),
 ("engines.s6.gate_tilt_deg", 10.0, "Gate: allowed |PnP tilt - IMU tilt| (only with a body-fixed camera)."),
 ("engines.s6.gate_yaw_deg", 15.0, "Gate: allowed |PnP yaw - compass yaw|."),
 ("engines.s6.max_res_px", 3.0, "Gate: maximum median reprojection residual (px)."),
 ("engines.s6.tracking", True, "After a lock, match only around the predicted position (cheaper and bridges blur/haze); falls back to global search after track_miss_reset misses."),
 ("engines.s6.track_miss_reset", 3, "Consecutive tracking failures before a global search."),
 ("engines.s6.track_vmax_mps", 30.0, "Tracking gate: maximum plausible ground speed."),
 ("engines.s6.track_margin_m", 40.0, "Tracking gate: extra jump allowance."),
 ("engines.s6.track_min_inl", 15, "Tracking: minimum inliers."),
 ("engines.s6.ref_cache", 400, "Reference-patch feature cache entries."),
 # --- S5
 ("engines.s5.repo_path", None, "Path of the cloned YuanJiayuuu/SWA-PF repository (no licence is declared, so it is never copied into this package; `nexnav setup --s5` clones it)."),
 ("engines.s5.n_particles", 20000, "Particles (accuracy needs >= 20,000 here; 1,000 locked on a wrong place)."),
 ("engines.s5.n_min", 1000, "Resampling floor."),
 ("engines.s5.label_res_m", 1.0, "Resolution of the semantic label map (m/px): 0.5, 1.0 or 2.0 tested."),
 ("engines.s5.label_gsd_m", 0.18, "Ground resolution (m/px) at which the segmenter sees map windows when the label map is built (match your camera GSD at the nominal altitude)."),
 ("engines.s5.label_map", None, "Optional .npy label map you built yourself (classes 0 ground,1 building,2 road,3 vegetation,4 vehicle,5 roof,6 water); otherwise it is built from the reference map with the repo's SegFormer and cached."),
 ("engines.s5.altitude_from_telemetry", True, "Pin the particle altitude to the altitude source (+/- altitude_sigma_m) instead of searching it: needed for a wide 100-500 m range."),
 ("engines.s5.altitude_sigma_m", 8.0, "Altitude noise of particles."),
 ("engines.s5.hmin_m", 50.0, "Lowest altitude (m) the filter may assume."),
 ("engines.s5.hmax_m", 550.0, "Highest altitude (m) the filter may assume; it also sets how much the map is padded (memory), so keep it just above your real ceiling."),
 ("engines.s5.init_mode", 0, "0 full random (most reliable here), 1 layered random, 2 semantic alignment."),
 ("engines.s5.sig_xy_m", 8.0, "Particle position noise per step (m)."),
 ("engines.s5.sig_yaw_deg", 0.2, "Particle yaw noise per step (deg)."),
 ("engines.s5.eps_m", 15.0, "DBSCAN radius (m) for the estimate."),
 ("engines.s5.min_samples", 20, "DBSCAN minimum samples."),
 ("engines.s5.conv_ratio", 0.10, "Converged when the particle spread falls below this fraction of the initial spread."),
 ("engines.s5.accept_frac", 0.5, "Share of particles that must sit in the main cluster for the estimate to be accepted."),
 ("engines.s5.cam_view", "auto", "Ground width of the square image crop per metre of altitude (auto = floor(min(W,H)/sqrt2)/fx)."),
 ("engines.s5.odometry", "telemetry", "telemetry (EKF/GPS velocity from the autopilot, heading from compass) | none (no motion model: not recommended)."),
 ("engines.s5.odometry_sigma_mps", 0.0, "Extra Gaussian noise added to the odometry velocity (bench tests only)."),
 # ---------------------------------------------------------------- fusion
 ("fusion.mode", "agree_or_best", "primary_fallback: primary engine, secondary only when the primary has no fix | agree_or_best: average when both agree, else the higher-priority/confidence one (flagged conflict) | consensus: output only when both agree."),
 ("fusion.primary", "s6", "Priority engine."),
 ("fusion.agree_radius_m", 30.0, "Two fixes closer than this agree."),
 ("fusion.weights", {"s6": 1.0, "s5": 1.0}, "Averaging weights when both agree."),
 ("fusion.max_fix_age_s", 3.0, "A fix older than this is not combined with a newer one from the other engine."),
 # ---------------------------------------------------------------- output
 ("output.save_frames_every", 0, "Save every Nth camera frame into the run folder (0 = never; frames are large)."),
 ("output.save_frame_scale", 0.5, "Scale of saved frames."),
 ("output.telemetry_log_hz", 10.0, "Rate of the telemetry CSV."),
 ("output.mavlink_gps_input.enabled", False, "Inject the fused fix into the autopilot as a second GPS (MAVLink GPS_INPUT). Leave OFF until tested in SITL."),
 ("output.mavlink_gps_input.connection", None, "Connection to send on (default: the telemetry connection)."),
 ("output.mavlink_gps_input.gps_id", 1, "GPS_INPUT gps_id (instance of the MAV-type GPS)."),
 ("output.mavlink_gps_input.max_rate_hz", 5.0, "Maximum injection rate."),
 ("output.mavlink_gps_input.horiz_accuracy_m", 15.0, "Reported horizontal accuracy."),
 ("output.mavlink_gps_input.only_when_gps_lost", True, "Send only while the real GPS fix is missing or below gps_min_fix_type."),
 ("output.mavlink_gps_input.project_forward", True, "Project each fix forward to the send time with the EKF velocity."),
]


def _set(d, key, val):
    parts = key.split("."); cur = d
    for p in parts[:-1]: cur = cur.setdefault(p, {})
    cur[parts[-1]] = copy.deepcopy(val)


def default_config():
    d = {}
    for k, v, _ in SCHEMA: _set(d, k, v)
    return d


_KEYS = {k for k, _, _ in SCHEMA}
# dict-valued leaves (their sub-keys are free-form)
_DICT_LEAVES = {"fusion.weights"}


def _flatten(d, prefix=""):
    for k, v in d.items():
        key = f"{prefix}{k}"
        if isinstance(v, dict) and key not in _DICT_LEAVES: yield from _flatten(v, key + ".")
        else: yield key, v


def deep_merge(base, over):
    for k, v in over.items():
        if isinstance(v, dict) and isinstance(base.get(k), dict) and not (k == "weights"): deep_merge(base[k], v)
        elif isinstance(v, dict) and isinstance(base.get(k), dict): base[k].update(v)
        else: base[k] = v
    return base


def validate(cfg):
    errs = []
    for key, _ in _flatten(cfg):
        if key not in _KEYS: errs.append(f"unknown config key: {key}")
    r = cfg["run"]
    eng = r["engines"] if isinstance(r["engines"], list) else [r["engines"]]
    if eng == ["both"]: eng = ["s6", "s5"]
    if not eng or any(e not in ("s6", "s5") for e in eng): errs.append("run.engines must be s6, s5 or both")
    r["engines"] = eng
    if cfg["map"]["path"] is None: errs.append("map.path is required")
    c = cfg["camera"]
    if c["fx"] is None and c["hfov_deg"] is None: errs.append("camera: give fx (pixels) or hfov_deg")
    if c["mount"] not in ("nadir_stabilised", "body_fixed"): errs.append("camera.mount must be nadir_stabilised or body_fixed")
    if cfg["telemetry"]["source"] not in ("mavlink", "csv", "fixed"): errs.append("telemetry.source must be mavlink, csv or fixed")
    if cfg["telemetry"]["source"] == "csv" and not cfg["telemetry"]["csv"]["path"]: errs.append("telemetry.csv.path is required for csv telemetry")
    if cfg["fusion"]["mode"] not in ("primary_fallback", "agree_or_best", "consensus"): errs.append("fusion.mode invalid")
    if len(eng) == 2 and cfg["fusion"]["primary"] not in eng: errs.append("fusion.primary must be one of run.engines")
    if "s5" in eng and not cfg["engines"]["s5"]["repo_path"]: errs.append("engines.s5.repo_path is required when s5 is enabled (run `nexnav setup --s5`)")
    if cfg["engines"]["device"] not in ("auto", "cuda", "cpu"): errs.append("engines.device must be auto, cuda or cpu")
    return errs


def load_config(path=None, overrides=None):
    cfg = default_config()
    if path:
        text = open(path).read()
        if path.endswith(".json"): user = json.loads(text)
        else:
            import yaml
            user = yaml.safe_load(text) or {}
        deep_merge(cfg, user)
    for k, v in (overrides or {}).items(): _set(cfg, k, v)
    errs = validate(cfg)
    if errs: raise ValueError("invalid configuration:\n  " + "\n  ".join(errs))
    return cfg


def expand(p):
    return None if p is None else os.path.expanduser(str(p))


def template_yaml():
    """A fully commented config with every parameter at its default."""
    import yaml
    out = ["# nexnav configuration (every key shown with its default). Delete what you do not change.\n"]
    section = None
    for k, v, doc in SCHEMA:
        top = k.split(".")[0] + "." + (k.split(".")[1] if k.count(".") >= 2 and k.split(".")[0] in ("engines",) else "")
        if top != section:
            section = top; out.append(f"\n# ---- {top.strip('.')} " + "-" * 40 + "\n")
        out.append(f"# {k}: {doc}\n#   {k} = {json.dumps(v)}\n")
    # real YAML body
    out.append("\n# ======== YAML body ========\n")
    out.append(yaml.safe_dump(default_config(), sort_keys=False, default_flow_style=None))
    return "".join(out)


def config_markdown():
    rows = ["| Key | Default | Meaning |", "|---|---|---|"]
    for k, v, doc in SCHEMA:
        rows.append(f"| `{k}` | `{json.dumps(v)}` | {doc} |")
    return "\n".join(rows)
