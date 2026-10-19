import os, sys, json
import numpy as np
import pytest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from geometry import *
import load_lidar as L
import build_map as BM
import register_to_blender as RB
import extract_floorplan as EF
from scipy.spatial import cKDTree

CFG = dict(sensor=dict(angle_unit_deg=0.01, range_unit_m=0.001, min_range_m=0.15, max_range_m=12.0, angle_sign=-1, angle_offset_deg=0.0))


def fake_log(a, r, t0=0.0, t1=0.07):
    return dict(a_raw=np.array(a, np.int32), r_raw=np.array(r, np.int32), t0=np.array([t0]), t1=np.array([t1]), offsets=np.array([0, len(a)]), skipped=np.array(0))


def test_angle_convention_clockwise_positive():
    d = fake_log([0, 9000, 18000, 27000], [1000, 2000, 3000, 4000])
    xy, _ = L.scan_xy(d, 0, CFG)
    # device angle is clockwise: 90 deg -> -y, 270 deg -> +y
    np.testing.assert_allclose(xy, [[1, 0], [0, -2], [-3, 0], [0, 4]], atol=1e-9)
    CFG2 = json.loads(json.dumps(CFG)); CFG2["sensor"]["angle_sign"] = 1
    xy2, _ = L.scan_xy(d, 0, CFG2)
    np.testing.assert_allclose(xy2[1], [0, 2], atol=1e-9)


def test_range_units_and_validity():
    d = fake_log([0, 100, 200], [100, 5000, 20000])          # 0.1 m (< min), 5 m, 20 m (> max)
    xy, t = L.scan_xy(d, 0, CFG)
    assert len(xy) == 1 and abs(np.hypot(*xy[0]) - 5.0) < 1e-9


def test_pose_algebra():
    a = np.array([1.0, 2.0, 0.7]); b = np.array([-0.5, 0.3, -1.2])
    np.testing.assert_allclose(compose(a, inverse(a)), 0, atol=1e-12)
    np.testing.assert_allclose(compose(a, relative(a, b)), b, atol=1e-12)
    p = np.array([[1.0, 0.0]]); np.testing.assert_allclose(transform([0, 0, np.pi / 2], p), [[0, 1]], atol=1e-12)
    np.testing.assert_allclose(pose_mat(a) @ np.linalg.inv(pose_mat(a)), np.eye(3), atol=1e-12)


def room(n=400, rng=np.random.default_rng(0)):
    """Points on the walls of an L-shaped room with noise 5 mm."""
    segs = [((0, 0), (6, 0)), ((6, 0), (6, 3)), ((6, 3), (3, 3)), ((3, 3), (3, 5)), ((3, 5), (0, 5)), ((0, 5), (0, 0))]
    pts = []
    for a, b in segs:
        t = rng.random((n, 1)); pts.append(np.array(a) + t * (np.array(b) - np.array(a)))
    return np.vstack(pts) + rng.normal(0, 0.005, (6 * n, 2))


def test_point_to_line_matching_recovers_known_transform():
    world = room(); sub = Submap(voxel_filter(world, 0.03))
    true = np.array([2.0, 1.5, np.deg2rad(20)])
    scan = transform(inverse(true), world[::3])                 # scan expressed in the sensor frame at `true`
    p, rms, inl, _ = sub.match(scan, true + np.array([0.15, -0.1, np.deg2rad(4)]))
    assert np.hypot(*(p[:2] - true[:2])) < 0.01 and abs(wrap(p[2] - true[2])) < np.deg2rad(0.3)
    assert inl > 0.95 and rms < 0.01


def test_rigid_fit_recovers_pose():
    model = room(); mt = cKDTree(model)
    true = np.array([0.3, -0.2, np.deg2rad(5)])
    pts = transform(inverse(true), model[::4])
    p, fit = RB.rigid_fit(pts, mt, model, np.array([0.0, 0.0, 0.0]))
    assert np.hypot(*(p[:2] - true[:2])) < 0.03 and abs(wrap(p[2] - true[2])) < np.deg2rad(0.5)
    assert fit["frac_within_10cm"] > 0.95


def test_global_search_finds_rotation():
    import global_reg as GR
    model = room(); true = np.deg2rad(37)
    pts = model[::3] @ rot(-true).T + 4.0
    r = GR.global_search(pts, model, res=0.1, dang=0.5, mirrors=(1,))
    assert abs((r[0][2] - 37 + 180) % 360 - 180) <= 1.0


def test_ply_roundtrip(tmp_path):
    xyz = np.random.default_rng(1).random((50, 3)).astype(np.float32); sid = np.arange(50, dtype=np.int32); t = np.linspace(0, 1, 50).astype(np.float32)
    BM.write_ply(str(tmp_path / "a.ply"), xyz, {"scan_id": (sid, "int"), "t": (t, "float")})
    r = BM.read_ply(str(tmp_path / "a.ply"))
    np.testing.assert_allclose(r["x"], xyz[:, 0]); assert (r["scan_id"] == sid).all()


def test_persistence_filter_drops_transients():
    rng = np.random.default_rng(2)
    wall = np.c_[rng.uniform(0, 5, 3000), rng.normal(0, 0.005, 3000)]; wid = rng.integers(0, 200, 3000)       # seen by many scans
    ghost = np.array([[2.0, 3.0]] * 5); gid = np.array([7, 7, 7, 7, 7])                                            # one scan only
    keep = BM.persistence_filter(np.vstack([wall, ghost]), np.r_[wid, gid], 0.05, 4)
    assert keep[:3000].mean() > 0.9 and not keep[3000:].any()


def test_face_detection_on_synthetic_rectangle():
    rng = np.random.default_rng(3); pts = []; sid = []
    for a, b in [((0, 0), (5, 0)), ((5, 0), (5, 3)), ((5, 3), (0, 3)), ((0, 3), (0, 0))]:
        t = rng.random((2500, 1)); pts.append(np.array(a) + t * (np.array(b) - np.array(a))); sid.append(rng.integers(0, 300, 2500))
    pts = np.vstack(pts) + rng.normal(0, 0.006, (10000, 2)); sid = np.concatenate(sid)
    sens = np.tile([2.5, 1.5], (300, 1))
    vp = voxel_filter(pts, 0.02)
    faces = EF.extend_to_corners(EF.merge_faces(EF.detect_faces(vp, rng.integers(0, 300, len(vp)), sens)))
    lens = sorted(f["length"] for f in faces)
    assert len(faces) == 4 and abs(lens[0] - 3.0) < 0.05 and abs(lens[-1] - 5.0) < 0.05


# ---- integration test on real data (skipped when the data is not present)
DATA = os.path.join(os.path.dirname(__file__), "..", "data", "lidar_h.jsonl")


@pytest.mark.skipif(not os.path.exists(DATA), reason="real LiDAR log not available")
def test_real_data_odometry_tracks():
    import itertools, estimate_trajectory as ET
    d = L.load_jsonl(DATA)
    n = 600
    d = dict(a_raw=d["a_raw"][:d["offsets"][n]], r_raw=d["r_raw"][:d["offsets"][n]], t0=d["t0"][:n], t1=d["t1"][:n], offsets=d["offsets"][:n + 1], skipped=d["skipped"])
    P, S, K = ET.run(CFG | {"slam": dict(min_scan_points=60, voxel_m=0.05)}, d, 0, n)
    ok = np.isfinite(S[:, 0])
    assert np.median(S[ok, 1]) > 0.9 and np.nanmedian(S[:, 0]) < 0.05
    assert np.hypot(*np.diff(P[:, :2], axis=0).T).max() < 0.5              # no teleporting between consecutive scans
