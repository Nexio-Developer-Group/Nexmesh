"""Diagnostic figures: scans, consecutive-scan registration, trajectory + uncertainty, accumulated cloud vs model, loop closures,
LiDAR-only vs anchored map, coverage gaps and registration residuals."""
import json, os
import numpy as np
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import load_lidar as L
from geometry import *
from scipy.spatial import cKDTree


def run(cfg, P):
    od = P(cfg, "diagnostics"); os.makedirs(od, exist_ok=True)
    d = L.load_jsonl(cfg["paths"]["lidar_jsonl"], P(cfg, "lidar_cache.npz")); an = np.load(P(cfg, "anchored.npz")); so = np.load(P(cfg, "slam_only.npz")); sp = np.load(P(cfg, "scan_poses.npz"))
    reg = json.load(open(P(cfg, "registration.json"))); seg = json.load(open(P(cfg, "model_slice.json"))); z = np.load(P(cfg, "processed_registered.npz"))
    poses = sp["poses"]; kf = an["kf_pose"]; sph = reg["sphere_world_xyz_m"]
    col = {"Wall": "#c33", "Column": "#a3a", "Window": "#3aa", "Door": "#3a3"}
    def model(ax, lw=.5, alpha=.8):
        for s in seg:
            if s["kind"] in col:
                ax.plot([s["p"][0], s["p"][2]], [s["p"][1], s["p"][3]], c=col[s["kind"]], lw=lw, alpha=alpha)
    xy = z["xy"][::4].astype(float)
    # 1. accumulated registered cloud over model
    fig, ax = plt.subplots(figsize=(14, 11)); ax.scatter(xy[:, 0], xy[:, 1], s=.05, c="k", alpha=.3, linewidths=0); model(ax); ax.plot(sph[0], sph[1], "bo", ms=8, label="start sphere")
    ax.set_aspect("equal"); ax.set_title("Registered point cloud (black) over Blender section at LiDAR height (colour)"); ax.legend(); fig.savefig(f"{od}/01_registered_cloud_vs_model.png", dpi=110, bbox_inches="tight"); plt.close(fig)
    # 2. trajectory coloured by time with uncertainty
    fig, ax = plt.subplots(1, 2, figsize=(20, 9)); model(ax[0], .4, .5)
    sc = ax[0].scatter(poses[::5, 0], poses[::5, 1], c=np.arange(0, len(poses), 5) / 13.33 / 60, s=1.2, cmap="viridis"); plt.colorbar(sc, ax=ax[0], label="minutes"); ax[0].plot(sph[0], sph[1], "ro"); ax[0].set_aspect("equal"); ax[0].set_title("Estimated trajectory (Blender world)")
    s2 = ax[1].scatter(kf[:, 0], kf[:, 1], c=an["sigma_xy"] * 1000, s=4, cmap="magma_r"); plt.colorbar(s2, ax=ax[1], label="sigma_xy (mm)"); ax[1].set_aspect("equal"); ax[1].set_title("Pose uncertainty per keyframe")
    fig.savefig(f"{od}/02_trajectory_uncertainty.png", dpi=100, bbox_inches="tight"); plt.close(fig)
    # 3. individual scans + sequential registration (3 consecutive scans in the registered frame)
    i0 = 12000; fig, ax = plt.subplots(1, 3, figsize=(21, 7))
    s0, _ = L.scan_xy(d, i0, cfg); ax[0].scatter(s0[:, 0], s0[:, 1], s=6); ax[0].plot(0, 0, "r^"); ax[0].set_aspect("equal"); ax[0].set_title("single raw scan, sensor frame (scan %d, %d pts)" % (i0, len(s0)))
    for k, c in zip(range(5), "rgbcm"):
        s, _ = L.scan_xy(d, i0 + k * 4, cfg); q = transform(poses[i0 + k * 4], s); ax[1].scatter(q[:, 0], q[:, 1], s=4, c=c)
    ax[1].set_aspect("equal"); ax[1].set_title("5 scans (every 4th) registered into the world frame")
    for k in range(i0, i0 + 60, 4):
        s, _ = L.scan_xy(d, k, cfg); q = transform(poses[k], s); ax[2].scatter(q[:, 0], q[:, 1], s=2, c="k", alpha=.4)
    ax[2].set_aspect("equal"); ax[2].set_title("15 scans accumulated: wall thickness of the point band = registration consistency")
    fig.savefig(f"{od}/03_scans_and_sequential_registration.png", dpi=90, bbox_inches="tight"); plt.close(fig)
    # 4. loop closures + LiDAR-only vs anchored
    fig, ax = plt.subplots(1, 2, figsize=(20, 10)); ks = so["kf_pose"]; ax[0].plot(ks[:, 0], ks[:, 1], "k-", lw=.4)
    for l in so["loops"]:
        i, j = int(l[0]), int(l[1]); ax[0].plot([ks[i, 0], ks[j, 0]], [ks[i, 1], ks[j, 1]], "r-", lw=.8)
    ax[0].set_aspect("equal"); ax[0].set_title("LiDAR-only SLAM frame: trajectory and %d verified loop closures (red)" % len(so["loops"]))
    odo = an["kf_pose_odom"]; ax[1].plot(odo[:, 0], odo[:, 1], c="#aaa", lw=.5, label="odometry (drifting, SLAM frame)"); ax[1].plot(ks[:, 0], ks[:, 1], "k-", lw=.5, label="LiDAR-only optimised"); ax[1].legend(); ax[1].set_aspect("equal"); ax[1].set_title("odometry vs LiDAR-only optimised (same frame)")
    fig.savefig(f"{od}/04_loop_closures_and_drift.png", dpi=90, bbox_inches="tight"); plt.close(fig)
    # 5. registration residuals: per-point distance to model, and coverage of model walls
    mp = np.vstack([(lambda a, b: a + (b - a) * np.linspace(0, 1, max(2, int(np.linalg.norm(b - a) / 0.05)))[:, None])(np.array(s["p"][:2]), np.array(s["p"][2:])) for s in seg if s["kind"] in ("Wall", "Column", "Window", "Door")])
    dd, _ = cKDTree(mp).query(xy, distance_upper_bound=0.5)
    fig, ax = plt.subplots(1, 2, figsize=(20, 9)); m = np.isfinite(dd)
    s3 = ax[0].scatter(xy[m, 0], xy[m, 1], c=dd[m] * 100, s=.2, cmap="turbo", vmin=0, vmax=15); plt.colorbar(s3, ax=ax[0], label="distance to model (cm)"); ax[0].set_aspect("equal"); ax[0].set_title("Registration residual per point (cm)")
    lt = cKDTree(xy); cov = []
    for s in seg:
        if s["kind"] in ("Wall", "Column"):
            a, b = np.array(s["p"][:2]), np.array(s["p"][2:]); n = max(2, int(np.linalg.norm(b - a) / .05)); sm = a + (b - a) * np.linspace(0, 1, n)[:, None]
            h = np.isfinite(lt.query(sm, distance_upper_bound=0.08)[0]); ax[1].plot(*zip(a, b), c="g" if h.mean() > .5 else "r", lw=1.2)
    ax[1].set_aspect("equal"); ax[1].set_title("Model walls: green = LiDAR-supported (>50% within 8 cm), red = poor/no coverage")
    fig.savefig(f"{od}/05_residuals_and_coverage.png", dpi=90, bbox_inches="tight"); plt.close(fig)
    # 6. LiDAR-only map (rigidly fitted) vs anchored
    pose = np.array(reg["lidar_only_map_rigid_fit"]["pose"]); odm = np.load(P(cfg, "scan_poses.npz"))["poses_slam_only"][::6]
    pts = []
    for i in range(0, len(odm), 4):
        s, _ = L.scan_xy(d, i * 6, cfg); pts.append(transform(pose, transform(odm[i], s)))
    pts = np.vstack(pts); fig, ax = plt.subplots(figsize=(13, 11)); ax.scatter(pts[:, 0], pts[:, 1], s=.05, c="k", alpha=.3, linewidths=0); model(ax)
    ax.set_aspect("equal"); ax.set_title("LiDAR-only SLAM map after best RIGID fit to model (%.0f%% within 10 cm): residual drift without anchoring" % (100 * reg["lidar_only_map_rigid_fit"]["frac_within_10cm"]))
    fig.savefig(f"{od}/06_lidar_only_rigid_fit_vs_model.png", dpi=100, bbox_inches="tight"); plt.close(fig)
