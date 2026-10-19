"""SIMULATED_3d_lidar_distance_btw_walls.dxf: every clear wall-to-wall distance computable from the simulated 3D point cloud's plan cut
(fitted wall faces, openings, mm dimensions). Illustrative, NOT measured. Also writes a CSV with the Blender-model value where a matching wall pair exists."""
import csv, json, os, sys
import numpy as np
import ezdxf
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wall_distances import pairs
from compare_distances_to_model import ray_hit


def run(root, out):
    O = os.path.join(root, "output"); S = os.path.join(out, "simulated_survey_grade")
    faces = json.load(open(f"{S}/SIMULATED_plan_faces.json")); prs = pairs(faces)
    seg = json.load(open(f"{O}/model_slice.json")); ms = [(np.array(q["p"][:2]), np.array(q["p"][2:])) for q in seg if q["kind"] in ("Wall", "Column", "Window")]
    doc = ezdxf.new("R2018", setup=True); doc.units = ezdxf.units.M; msp = doc.modelspace()
    for name, col in (("SIM_WALL_FACES", 8), ("SIM_DIM_WALL_TO_WALL", 1), ("SIM_DIM_TEXT", 3), ("SIM_NOTE", 1)): doc.layers.add(name, color=col)
    for f in faces: msp.add_line(f["p0"], f["p1"], dxfattribs={"layer": "SIM_WALL_FACES"})
    rows = []
    for p in prs:
        pa, pb = p["pa"], p["pb"]; v = (pb - pa) / np.linalg.norm(pb - pa); nn = np.array([-v[1], v[0]]); mid = (pa + pb) / 2; h = p["dist"] / 2
        msp.add_line(tuple(pa), tuple(pb), dxfattribs={"layer": "SIM_DIM_WALL_TO_WALL"})
        for pt in (pa, pb): msp.add_line(tuple(pt - nn * 0.08), tuple(pt + nn * 0.08), dxfattribs={"layer": "SIM_DIM_WALL_TO_WALL"})
        ang = np.degrees(np.arctan2(v[1], v[0])); ang = ang if -90 < ang <= 90 else ang + 180
        msp.add_text("%d" % (round(p["dist"] * 100) * 10), height=0.11, rotation=ang, dxfattribs={"layer": "SIM_DIM_TEXT"}).set_placement(tuple(mid + nn * 0.10), align=ezdxf.enums.TextEntityAlignment.MIDDLE_CENTER)
        t1 = ray_hit(mid, v, ms, h + 0.15); t2 = ray_hit(mid, -v, ms, h + 0.15); ok = t1 is not None and t2 is not None and abs(t1 - h) < .15 and abs(t2 - h) < .15
        rows.append((p["a"], p["b"], p["dist"] * 1000, (t1 + t2) * 1000 if ok else None))
    allp = np.array([f["p0"] for f in faces] + [f["p1"] for f in faces]); x0, y0 = allp.min(0)
    msp.add_text("SIMULATED - NOT MEASURED DATA: wall-to-wall distances (mm) from the simulated 3D LiDAR point cloud (plan cut z 1.0-1.6 m)", height=0.4, dxfattribs={"layer": "SIM_NOTE"}).set_placement((x0, y0 - 1.5))
    for d in (S, out): doc.saveas(os.path.join(d, "SIMULATED_3d_lidar_distance_btw_walls.dxf"))
    with open(f"{S}/SIMULATED_3d_lidar_distance_btw_walls.csv", "w", newline="") as f:
        w = csv.writer(f); w.writerow(["face_a", "face_b", "simulated_lidar_mm", "blender_model_mm", "simulated_minus_model_mm"])
        for a, b, d, m in rows: w.writerow([a, b, round(d), "" if m is None else round(m), "" if m is None else round(d - m)])
    print("%d wall-to-wall distances in the DXF (%d with a Blender-model match)" % (len(rows), sum(r[3] is not None for r in rows)))


if __name__ == "__main__":
    root = os.path.dirname(os.path.abspath(__file__)); run(root, os.path.join(root, "..", "poc_demo_files_output"))
