"""Rebuild the SIMULATED 2D cloud and 3D cloud for the CAD-referenced target readings, measure the readings back from the rebuilt 3D cloud and correct the face shifts until
they match the targets (closed loop). Then regenerate the plan, DXF/CSV and comparison map."""
import json, os, sys
import numpy as np
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_map as BM
import poc_cad_targets as CT
import poc_simulated_survey_grade as P2
import poc_simulated_3d as P3
from wall_distances import pairs


def measure(S, g, fs, T):
    ply = BM.read_ply(f"{S}/SIMULATED_3D_point_cloud.ply"); z = ply["z"].astype(float); m = (ply["kind"] == 1) & (z >= 1.0) & (z <= 1.6); xy = np.c_[ply["x"], ply["y"]][m].astype(float)
    F = {f["id"]: f for f in g["faces"]}; out = []
    for t in T:
        fa, fb = F[t["a"]], F[t["b"]]; na = np.array(fa["normal"]); da = np.array([na[1], -na[0]]); pa0 = np.array(fa["p0"]) + na * fs[t["a"]]
        ta = sorted([np.array(fa["p0"]) @ da, np.array(fa["p1"]) @ da]); tb = sorted([np.array(fb["p0"]) @ da, np.array(fb["p1"]) @ da]); lo, hi = max(ta[0], tb[0]), min(ta[1], tb[1])
        pos = {}
        for key, f, sgn in (("a", fa, na), ("b", fb, np.array(fb["normal"]))):
            nn = np.array(f["normal"]); q0 = np.array(f["p0"]) + nn * fs[t[key]]; tt = xy @ da; off = (xy - q0) @ nn
            sel = (np.abs(off) < 0.02) & (tt >= lo) & (tt <= hi)
            if sel.sum() < 8: pos[key] = None; continue
            pos[key] = float(np.median(xy[sel] @ na))
        out.append(None if pos["a"] is None or pos["b"] is None else (pos["b"] - pos["a"]) * 1000)
    return out


def run(root, out):
    S = os.path.join(out, "simulated_survey_grade"); O = os.path.join(root, "output"); g = json.load(open(f"{O}/floorplan_geometry.json"))
    T = json.load(open(f"{S}/SIMULATED_target_readings.json")); fs = json.load(open(f"{S}/SIMULATED_face_shifts_override.json"))
    ids = sorted({t["a"] for t in T} | {t["b"] for t in T}); ix = {f: i for i, f in enumerate(ids)}
    A = np.zeros((len(T), len(ids)))
    for r, t in enumerate(T): A[r, ix[t["a"]]] = 1; A[r, ix[t["b"]]] = 1
    for it in range(5):
        json.dump(fs, open(f"{S}/SIMULATED_face_shifts_override.json", "w")); P2.run(root, out); P3.run(root, S)
        meas = measure(S, g, fs, T); res = np.array([np.nan if m is None else (m - t["cad"]) - t["delta"] for m, t in zip(meas, T)])
        print("iteration %d: residual vs target: max %.1f mm, median %.1f mm, missing %d" % (it, np.nanmax(np.abs(res)), np.nanmedian(np.abs(res)), int(np.isnan(res).sum())))
        if np.nanmax(np.abs(res)) <= 2.5 and not np.isnan(res).any(): break
        r = np.nan_to_num(res) / 1000; dx = np.linalg.solve(A.T @ A + 1e-3 * np.eye(len(ids)), A.T @ r)
        fs = dict(fs); fs.update({f: fs.get(f, 0.0) + float(dx[ix[f]]) for f in ids})
    for t, m in zip(T, meas): t["sim_measured"] = m
    json.dump(T, open(f"{S}/SIMULATED_target_readings.json", "w"), indent=1)


if __name__ == "__main__":
    root = os.path.dirname(os.path.abspath(__file__)); run(root, os.path.join(root, "..", "poc_demo_files_output"))
