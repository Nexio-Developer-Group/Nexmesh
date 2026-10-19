"""Front-end: scan-to-submap odometry with keyframes. Writes trajectory in an arbitrary SLAM frame (first pose = identity)."""
import sys, time, argparse, logging, yaml
import numpy as np
import load_lidar as L
from geometry import *

log = logging.getLogger("traj")


def correlative_recover(sub, scan, pred, rad=0.25, ang=np.deg2rad(8)):
    best = (-1, pred)
    for dth in np.arange(-ang, ang + 1e-9, np.deg2rad(1.5)):
        for dx in np.arange(-rad, rad + 1e-9, 0.05):
            for dy in np.arange(-rad, rad + 1e-9, 0.05):
                p = np.array([pred[0] + dx, pred[1] + dy, pred[2] + dth])
                s = sub.score(scan, p)
                if s > best[0]:
                    best = (s, p)
    return best[1]


def run(cfg, d, i0=0, i1=None, kf_dist=0.3, kf_ang=np.deg2rad(10), nsub=35):
    n = len(d["t0"]); i1 = n if i1 is None else i1
    vox = cfg["slam"]["voxel_m"]
    poses, stats, kfs = [], [], []   # kfs: (scan idx, pose, pts_in_sensor_frame)
    prev = None; vel = np.zeros(3); sub = None; last_kf = None
    t_start = time.time()
    for i in range(i0, i1):
        scan, _ = L.scan_xy(d, i, cfg)
        if len(scan) < cfg["slam"]["min_scan_points"]:
            poses.append(poses[-1] if poses else np.zeros(3)); stats.append((np.nan, 0, 0, 0)); continue
        if sub is None:
            p = np.zeros(3) if prev is None else prev
            rms, inl, fail = 0.0, 1.0, 0
        else:
            dt = d["t0"][i] - d["t0"][i - 1]
            pred = prev + vel * min(dt, 0.2) * 0.8 if False else prev + vel
            p, rms, inl, _ = sub.match(scan, pred)
            fail = 0
            if inl < 0.55 or rms > 0.06:
                p2 = correlative_recover(sub, scan, pred)
                p2, rms2, inl2, _ = sub.match(scan, p2)
                if inl2 > inl:
                    p, rms, inl = p2, rms2, inl2
                fail = 1
            vel = np.r_[p[:2] - prev[:2], wrap(p[2] - prev[2])]
            vel = np.clip(vel, [-0.3, -0.3, -0.3], [0.3, 0.3, 0.3])
        prev = p
        poses.append(p); stats.append((rms, inl, fail, len(scan)))
        if last_kf is None or np.hypot(*(p[:2] - last_kf[:2])) > kf_dist or abs(wrap(p[2] - last_kf[2])) > kf_ang:
            kfs.append((i, p.copy(), scan)); last_kf = p
            pts = np.vstack([transform(k[1], k[2]) for k in kfs[-nsub:]])
            near = np.hypot(*(pts - p[:2]).T) < 15
            sub = Submap(voxel_filter(pts[near], vox))
        if (i - i0) % 2000 == 0:
            log.info("scan %d/%d  %.1fs  kf=%d  inl=%.2f", i, i1, time.time() - t_start, len(kfs), inl)
    return np.array(poses), np.array(stats), kfs


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(message)s")
    ap = argparse.ArgumentParser()
    ap.add_argument("--config", default="config.yaml"); ap.add_argument("--lidar"); ap.add_argument("--out")
    ap.add_argument("--n", type=int); ap.add_argument("--sign", type=int)
    a = ap.parse_args()
    cfg = yaml.safe_load(open(a.config))
    if a.sign: cfg["sensor"]["angle_sign"] = a.sign
    d = L.load_jsonl(a.lidar, a.out + "_cache.npz")
    P, S, K = run(cfg, d, 0, a.n)
    np.savez(a.out + "_odom.npz", poses=P, stats=S, kf_idx=np.array([k[0] for k in K]), kf_pose=np.array([k[1] for k in K]))
