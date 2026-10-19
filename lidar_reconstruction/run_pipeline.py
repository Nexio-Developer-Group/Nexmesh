"""End-to-end driver.  python run_pipeline.py --config config.yaml [--stages inventory,model,odometry,slam,register,map,floorplan,blender,validate] [--force]
Every stage caches its result in work_dir so a failure does not require restarting from scratch."""
import argparse, json, logging, os, sys, time
import numpy as np
import yaml

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import load_lidar as L
import estimate_trajectory as ET
import pose_graph as PG
import graph_solver as GS
import model_anchor as MA
import register_to_blender as RB
import build_map as BM
from geometry import *
from scipy.spatial import cKDTree

log = logging.getLogger("pipeline")
STAGES = ["inventory", "model", "odometry", "slam", "register", "map", "floorplan", "blender", "diagnostics", "validate"]


def P(cfg, name):
    return os.path.join(cfg["_work"], name)


def load_cfg(path):
    cfg = yaml.safe_load(open(path)); base = os.path.dirname(os.path.abspath(path))
    for k in ("blend", "lidar_jsonl", "work_dir"):
        if not os.path.isabs(cfg["paths"][k]):
            cfg["paths"][k] = os.path.normpath(os.path.join(base, cfg["paths"][k]))
    cfg["_work"] = cfg["paths"]["work_dir"]; os.makedirs(cfg["_work"], exist_ok=True)
    return cfg


def stage_inventory(cfg):
    import inspect_inputs
    inv = inspect_inputs.inspect(cfg["paths"]["blend"])
    sph = [o for o in inv["objects"] if o["name"] == cfg["scene"]["sphere_name"]]
    spheres = [o for o in inv["objects"] if o["type"] == "MESH" and "sphere" in o["name"].lower()]
    assert len(spheres) == 1, "ambiguous / missing sphere: %s" % [o["name"] for o in spheres]
    d = L.load_jsonl(cfg["paths"]["lidar_jsonl"], P(cfg, "lidar_cache.npz"))
    n = np.diff(d["offsets"]); dt = np.diff(d["t0"])
    inv["sphere"] = spheres[0]
    inv["lidar"] = dict(file=os.path.basename(cfg["paths"]["lidar_jsonl"]), bytes=os.path.getsize(cfg["paths"]["lidar_jsonl"]),
                        scans=int(len(n)), skipped_records=int(d["skipped"]), points=int(n.sum()),
                        t_start=float(d["t0"][0]), t_end=float(d["t1"][-1]), duration_s=float(d["t1"][-1] - d["t0"][0]),
                        scan_rate_hz=float(1 / np.median(dt)), points_per_scan=[int(n.min()), float(np.median(n)), int(n.max())],
                        scans_under_60_pts=int((n < 60).sum()), timestamps_monotonic=bool(np.all(dt >= 0)),
                        max_gap_s=float(dt.max()), range_raw_min_max=[int(d["r_raw"].min()), int(d["r_raw"].max())],
                        angle_raw_min_max=[int(d["a_raw"].min()), int(d["a_raw"].max())],
                        motion_sensors="none (no IMU/odometry fields: keys are t0,t1,a,r only)",
                        beam_angle_spacing_deg_median=1.3)
    json.dump(inv, open(P(cfg, "inventory.json"), "w"), indent=1, default=str)
    return inv


def sensor_height(cfg, inv):
    return cfg["scene"]["lidar_height_m"] or inv["sphere"]["world_loc"][2]


def stage_model(cfg, inv):
    import model_extract
    z = cfg["scene"]["slice_z_m"] or sensor_height(cfg, inv)
    model_extract.slice_segments(cfg["paths"]["blend"], z, P(cfg, "model_slice.json"), skip=(inv["sphere"]["name"],))
    model_extract.extract_text(cfg["paths"]["blend"], P(cfg, "model_text.json"))


def stage_odometry(cfg):
    d = L.load_jsonl(cfg["paths"]["lidar_jsonl"], P(cfg, "lidar_cache.npz"))
    s = cfg["slam"]
    poses, stats, kfs = ET.run(cfg, d, kf_dist=s["keyframe_dist_m"], kf_ang=np.deg2rad(s["keyframe_yaw_deg"]), nsub=s["submap_keyframes"])
    np.savez(P(cfg, "odometry.npz"), poses=poses, stats=stats, kf_idx=np.array([k[0] for k in kfs]), kf_pose=np.array([k[1] for k in kfs]))


def kf_scans(cfg, d, kf_idx):
    return [L.scan_xy(d, i, cfg)[0] for i in kf_idx]


def stage_slam(cfg):
    """LiDAR-only back-end: Manhattan heading prior + loop closures + pose graph. No model information is used."""
    d = L.load_jsonl(cfg["paths"]["lidar_jsonl"], P(cfg, "lidar_cache.npz")); z = np.load(P(cfg, "odometry.npz"))
    kp, ki = z["kf_pose"], z["kf_idx"]; s = cfg["slam"]
    sc = kf_scans(cfg, d, ki)
    maps = PG.local_maps(sc, kp, w=s["local_map_half"], rmax=10.0)
    al, stg = PG.manhattan_headings(PG.local_maps(sc, kp, w=8))
    strong = stg > 0.6
    phi = np.angle(np.nanmean(np.exp(4j * (kp[:, 2] + al))[strong])) / 4
    odo = [(k, k + 1, relative(kp[k], kp[k + 1])) for k in range(len(kp) - 1)]
    hd = [(k, al[k], phi) for k in range(len(kp)) if strong[k]]
    # pass 1: heading only (removes rotational drift), then loop search on the corrected poses
    X1, _ = GS.solve(kp, odo, hd, [], [], (0.05, np.deg2rad(0.8)), (0.06, np.deg2rad(0.7)), max_nfev=100)
    loops, ntried = PG.find_loops(maps, X1, min_sep=s["loop_min_separation_kf"], radius=s["loop_radius_m"], step=4)
    loops = [(l[0], l[1], l[2]) for l in loops]; n0 = len(loops)
    for it in range(3):
        X, sol = GS.solve(kp, odo, hd, loops, [], (0.05, np.deg2rad(0.8)), (0.06, np.deg2rad(0.7)), max_nfev=100)
        keep = []
        for (i, j, zr) in loops:      # chi of each loop edge at the solution
            dt = rot(X[i, 2]).T @ (X[j, :2] - X[i, :2]); e = np.r_[(dt - zr[:2]) / 0.06, wrap(X[j, 2] - X[i, 2] - zr[2]) / np.deg2rad(0.7)]
            keep.append(np.linalg.norm(e) < 4.0)
        if all(keep):
            break
        loops = [l for l, k in zip(loops, keep) if k]
    np.savez(P(cfg, "slam_only.npz"), kf_pose=X, heading_alpha=al, heading_strength=stg, phi=phi,
             loops=np.array([[l[0], l[1], *l[2]] for l in loops]), loop_candidates=ntried, loops_before_check=n0,
             kf_pose_odom=kp)
    log.info("SLAM-only: loops accepted %d/%d (from %d pairs), phi=%.2f deg", len(loops), n0, ntried, np.rad2deg(phi))


def stage_register(cfg):
    inv = json.load(open(P(cfg, "inventory.json")))
    d = L.load_jsonl(cfg["paths"]["lidar_jsonl"], P(cfg, "lidar_cache.npz")); z = np.load(P(cfg, "odometry.npz")); so = np.load(P(cfg, "slam_only.npz"))
    kp, ki = z["kf_pose"], z["kf_idx"]; sc = kf_scans(cfg, d, ki)
    sphere = np.array(inv["sphere"]["world_loc"]); a = cfg["anchor"]
    model = MA.model_points(P(cfg, "model_slice.json"))
    # 1. where does the start fit the model, globally and without any positional prior?
    first = np.vstack([transform(relative(kp[0], kp[m]), sc[m]) for m in range(12)])
    ranked, chosen = RB.locate_start(first, model, sphere[:2])
    assert chosen is not None, "no global start candidate within 2 m of the sphere; sphere/heading ambiguous - ask user for a reference direction"
    T0 = np.r_[chosen[2], np.deg2rad(chosen[1])]
    # 2. LiDAR-only map rigidly fitted to the model (diagnostic: how much drift remains without model anchoring)
    X_so = so["kf_pose"]
    pts_so = voxel_filter(np.vstack([transform(X_so[k], sc[k]) for k in range(len(X_so))]), 0.1)
    mtree = cKDTree(model[::2])
    p_so, fit_so = RB.rigid_fit(pts_so, mtree, model[::2], T0)
    # 3. model-anchored trajectory
    al, stg = so["heading_alpha"], so["heading_strength"]
    odo = [(k, k + 1, relative(kp[k], kp[k + 1])) for k in range(len(kp) - 1)]
    hd = [(k, al[k], 0.0) for k in range(len(kp)) if stg[k] > 0.6]
    loops = [(int(l[0]), int(l[1]), l[2:5]) for l in so["loops"]]
    X, anc, info = RB.anchor_trajectory(sc, kp, model, sphere[:2], T0, odo, hd, loops, rounds=tuple(a["rounds_window_m"]))
    P_all = np.vstack([transform(X[k], sc[k]) for k in range(0, len(X))])
    dd, _ = mtree.query(P_all, distance_upper_bound=1.0)
    fit_anch = {f"frac_within_{int(t*100)}cm": float(np.mean(dd < t)) for t in (0.05, 0.10, 0.20, 0.30)}
    # per-keyframe confidence
    sig_xy = np.full(len(X), np.nan); sig_yaw = np.full(len(X), np.nan); a_inl = np.zeros(len(X)); a_rms = np.full(len(X), np.nan)
    cs = np.array(sorted(anc))
    for k in range(len(X)):
        c = cs[np.argmin(np.abs(cs - k))]; p, H, inl, rms = anc[c]
        cov = np.linalg.inv(H / 0.03 ** 2 / 50.0 + np.eye(3) * 1e-6)
        gap = abs(c - k) * 0.3
        sig_xy[k] = np.sqrt(0.5 * (cov[0, 0] + cov[1, 1])) + 0.01 * gap; sig_yaw[k] = np.rad2deg(np.sqrt(cov[2, 2])) + 0.2 * gap
        a_inl[k] = inl; a_rms[k] = rms
    np.savez(P(cfg, "anchored.npz"), kf_pose=X, kf_idx=ki, kf_pose_odom=kp, sigma_xy=sig_xy, sigma_yaw_deg=sig_yaw,
             anchor_inlier=a_inl, anchor_rms=a_rms, anchor_chunks=cs)
    rep = dict(sphere_world_xyz_m=sphere.tolist(), start_candidates_ranked=[dict(score=r[0], yaw_deg=r[1], xy=r[2].tolist(),
               dist_to_sphere_m=float(np.hypot(*(r[2] - sphere[:2])))) for r in ranked], chosen_start=dict(score=chosen[0], yaw_deg=chosen[1], xy=chosen[2].tolist()),
               T0_slam_to_model=dict(x=T0[0], y=T0[1], yaw_deg=float(np.rad2deg(T0[2])), matrix=pose_mat(T0).tolist()),
               first_pose_after_anchoring=dict(xy=X[0, :2].tolist(), yaw_deg=float(np.rad2deg(X[0, 2])), offset_from_sphere_m=float(np.hypot(*(X[0, :2] - sphere[:2])))),
               start_constraint=dict(type="soft prior on x,y", sigma_m=a["start_sigma_m"]),
               scale="fixed 1.0 (metric LiDAR vs metric model; not estimated)", mirror="none (angle_sign=-1)",
               lidar_only_map_rigid_fit=dict(pose=p_so.tolist(), **fit_so, note="best rigid fit of the unanchored SLAM-only map; shortfall vs anchored = residual drift"),
               model_anchored_fit=fit_anch, anchoring_rounds=info, anchors=len(cs),
               model_section_height_m=float(inv["sphere"]["world_loc"][2]),
               limitations=["Anchoring uses the model, so LiDAR-vs-model residuals are not independent evidence of absolute accuracy.",
                            "Sphere XY is a soft constraint (sigma ~ sphere radius); heading comes from the data, not the sphere.",
                            "Model-vs-reality differences (furniture, open doors, as-built deviations) lower the inlier fraction."])
    json.dump(rep, open(P(cfg, "registration.json"), "w"), indent=1)
    log.info("registration: start offset from sphere %.3f m, anchored frac<=10cm %.3f, LiDAR-only rigid frac<=10cm %.3f",
             rep["first_pose_after_anchoring"]["offset_from_sphere_m"], fit_anch["frac_within_10cm"], fit_so["frac_within_10cm"])


def stage_map(cfg):
    inv = json.load(open(P(cfg, "inventory.json")))
    d = L.load_jsonl(cfg["paths"]["lidar_jsonl"], P(cfg, "lidar_cache.npz")); od = np.load(P(cfg, "odometry.npz")); an = np.load(P(cfg, "anchored.npz")); so = np.load(P(cfg, "slam_only.npz"))
    zs = sensor_height(cfg, inv); c = cfg["cloud"]
    reg = json.load(open(P(cfg, "registration.json"))); T0 = np.array(reg["T0_slam_to_model"]["matrix"])
    poses, kf_of = BM.scan_poses(od["poses"], an["kf_idx"], an["kf_pose_odom"], an["kf_pose"])
    cloud = BM.accumulate(d, cfg, poses, zs)
    np.savez_compressed(P(cfg, "raw_derived_registered.npz"), **cloud, note=np.array("all valid returns, Blender world frame (m); z = assumed sensor height"))
    keep = BM.persistence_filter(cloud["xy"], cloud["scan_id"], c["persistence_cell_m"], c["persistence_min_scans"])
    xy, sid, tt = cloud["xy"][keep], cloud["scan_id"][keep], cloud["t"][keep]
    v = voxel_filter(xy, c["processed_voxel_m"]); idx = cKDTree(xy).query(v)[1]
    xyz = np.c_[xy[idx], np.full(len(idx), zs, np.float32)]
    BM.write_ply(P(cfg, "registered_processed.ply"), xyz, {"scan_id": (sid[idx], "int"), "t": ((tt[idx] - d["t0"][0]).astype(np.float32), "float")})
    np.savez_compressed(P(cfg, "processed_registered.npz"), xy=xy, scan_id=sid, t=tt)
    # LiDAR-only (unanchored) map in its own SLAM frame
    kf_so = so["kf_pose"]; poses_so, _ = BM.scan_poses(od["poses"], an["kf_idx"], so["kf_pose_odom"], kf_so)
    cs = BM.accumulate(d, cfg, poses_so, zs, step=3)
    vs = voxel_filter(cs["xy"], 0.03)
    BM.write_ply(P(cfg, "slam_only_unregistered.ply"), np.c_[vs, np.zeros(len(vs))], {})
    # trajectories
    import csv
    for name, pz, X_kf in (("trajectory_registered.csv", poses, an), ("trajectory_slam_only.csv", poses_so, None)):
        with open(P(cfg, name), "w", newline="") as f:
            w = csv.writer(f)
            if X_kf is not None:
                w.writerow(["scan_id", "t_s", "x_m", "y_m", "z_m_assumed", "yaw_deg", "sigma_xy_m", "sigma_yaw_deg", "anchor_inlier_frac", "keyframe"])
                for i in range(len(pz)):
                    k = kf_of[i]
                    w.writerow([i, "%.4f" % d["t0"][i], "%.4f" % pz[i, 0], "%.4f" % pz[i, 1], "%.4f" % zs, "%.3f" % np.rad2deg(pz[i, 2]),
                                "%.4f" % an["sigma_xy"][k], "%.3f" % an["sigma_yaw_deg"][k], "%.3f" % an["anchor_inlier"][k], k])
            else:
                w.writerow(["scan_id", "t_s", "x_m", "y_m", "yaw_deg", "frame"])
                for i in range(len(pz)):
                    w.writerow([i, "%.4f" % d["t0"][i], "%.4f" % pz[i, 0], "%.4f" % pz[i, 1], "%.3f" % np.rad2deg(pz[i, 2]), "SLAM frame (first pose = origin), LiDAR-only"])
    np.savez(P(cfg, "scan_poses.npz"), poses=poses, poses_slam_only=poses_so, kf_of=kf_of)
    log.info("map: raw %d pts, persistent %d, processed %d", len(cloud["xy"]), int(keep.sum()), len(v))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--config", default=os.path.join(HERE, "config.yaml")); ap.add_argument("--stages", default=",".join(STAGES)); ap.add_argument("--force", action="store_true")
    a = ap.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(message)s", handlers=[logging.StreamHandler(), logging.FileHandler(os.path.join(os.path.dirname(os.path.abspath(a.config)), "output", "pipeline.log") if os.path.isdir(os.path.join(os.path.dirname(os.path.abspath(a.config)), "output")) else "pipeline.log")])
    cfg = load_cfg(a.config)
    inv = None
    done = {"inventory": "inventory.json", "model": "model_slice.json", "odometry": "odometry.npz", "slam": "slam_only.npz", "register": "registration.json", "map": "scan_poses.npz",
            "floorplan": "floorplan_geometry.json", "blender": "registered_scene.blend", "diagnostics": "diagnostics/05_residuals_and_coverage.png", "validate": "validation_report.json"}
    for st in a.stages.split(","):
        if not a.force and os.path.exists(P(cfg, done[st])):
            log.info("stage %s: cached (%s), skipping", st, done[st]); continue
        t = time.time(); log.info("=== stage %s ===", st)
        if st == "inventory": stage_inventory(cfg)
        elif st == "model": stage_model(cfg, json.load(open(P(cfg, "inventory.json"))))
        elif st == "odometry": stage_odometry(cfg)
        elif st == "slam": stage_slam(cfg)
        elif st == "register": stage_register(cfg)
        elif st == "map": stage_map(cfg)
        elif st == "floorplan":
            import extract_floorplan; extract_floorplan.run(cfg, P)
        elif st == "blender":
            import blender_integration; blender_integration.run(cfg, P)
        elif st == "diagnostics":
            import diagnostics; diagnostics.run(cfg, P)
        elif st == "validate":
            import validate_results; validate_results.run(cfg, P)
        log.info("stage %s done in %.1fs", st, time.time() - t)


if __name__ == "__main__":
    main()
