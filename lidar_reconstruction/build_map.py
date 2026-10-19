"""Scan poses, point-cloud accumulation, persistence filtering, PLY export."""
import numpy as np
import load_lidar as L
from geometry import *


def scan_poses(odom_poses, kf_idx, kf_odom, X_kf):
    """Pose of every scan = optimised keyframe pose composed with the scan's odometry pose relative to that keyframe."""
    n = len(odom_poses); out = np.empty((n, 3)); kf_of = np.searchsorted(kf_idx, np.arange(n), side="right") - 1
    kf_of = np.clip(kf_of, 0, len(kf_idx) - 1)
    for i in range(n):
        k = kf_of[i]
        out[i] = compose(X_kf[k], relative(kf_odom[k], odom_poses[i]))
    return out, kf_of


def accumulate(d, cfg, poses, z_sensor, step=1, deskew=False):
    """All valid points in the map frame with scan id and time. Returns dict of arrays (float32 xyz)."""
    P, sid, tt = [], [], []
    for i in range(0, len(poses), step):
        s, t = L.scan_xy(d, i, cfg)
        if len(s) == 0:
            continue
        P.append(transform(poses[i], s).astype(np.float32)); sid.append(np.full(len(s), i, np.int32)); tt.append(t.astype(np.float64))
    P = np.vstack(P); sid = np.concatenate(sid); tt = np.concatenate(tt)
    return dict(xy=P, z=np.full(len(P), z_sensor, np.float32), scan_id=sid, t=tt)


def persistence_filter(pts, scan_id, cell=0.05, min_scans=4):
    """Keep points whose cell was observed in >= min_scans distinct scans spread over time (static structure)."""
    key = np.floor(pts / cell).astype(np.int64); k = key[:, 0] * 2000003 + key[:, 1]
    order = np.argsort(k); ks = k[order]; ss = scan_id[order]
    start = np.r_[0, np.flatnonzero(np.diff(ks)) + 1]; end = np.r_[start[1:], len(ks)]
    keep = np.zeros(len(pts), bool)
    for a, b in zip(start, end):
        u = np.unique(ss[a:b])
        if len(u) >= min_scans and (u[-1] - u[0]) >= 3:
            keep[order[a:b]] = True
    return keep


def write_ply(path, xyz, fields=None):
    """Binary little-endian PLY. fields: dict name -> (array, ply_type)."""
    fields = fields or {}
    n = len(xyz)
    names = ["x", "y", "z"] + list(fields)
    dt = [("x", "<f4"), ("y", "<f4"), ("z", "<f4")] + [(k, "<" + {"float": "f4", "double": "f8", "int": "i4", "uchar": "u1"}[v[1]]) for k, v in fields.items()]
    arr = np.empty(n, dtype=dt)
    arr["x"], arr["y"], arr["z"] = xyz[:, 0], xyz[:, 1], xyz[:, 2]
    for k, v in fields.items():
        arr[k] = v[0]
    head = "ply\nformat binary_little_endian 1.0\ncomment frame: Blender world, metres\nelement vertex %d\n" % n
    head += "".join("property %s %s\n" % (t, nm) for nm, t in [(k, "float") for k in ("x", "y", "z")] + [(k, v[1]) for k, v in fields.items()])
    head += "end_header\n"
    with open(path, "wb") as f:
        f.write(head.encode()); f.write(arr.tobytes())


def read_ply(path):
    with open(path, "rb") as f:
        hdr = []
        while True:
            l = f.readline().decode().strip(); hdr.append(l)
            if l == "end_header":
                break
        n = int([h for h in hdr if h.startswith("element vertex")][0].split()[-1])
        props = [h.split() for h in hdr if h.startswith("property")]
        tm = {"float": "<f4", "double": "<f8", "int": "<i4", "uchar": "u1"}
        arr = np.frombuffer(f.read(), dtype=[(p[2], tm[p[1]]) for p in props], count=n)
    return arr
