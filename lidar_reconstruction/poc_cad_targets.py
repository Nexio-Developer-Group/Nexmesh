"""Target set of CAD-referenced readings for the SIMULATED data (illustrative, NOT measured) and the wall shifts that realise it.
Red (>15 mm) kept: flat-1 / flat-2 kitchens (CAD 2400), flat-3 hall (CAD 7070), flat-4 hall (7070 by symmetry).
Hallway readings (lobby/passage/lift/corridor) removed; in-room readings only; the rest green (<=7 mm) or blue (7-15 mm, fewer)."""
import csv, json, os, sys
import numpy as np
from shapely.geometry import Polygon, Point
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wall_distances import pairs

ROOMS_OK = {"BEDROOM - 1", "BEDROOM - 2", "KITCHEN", "LIVING / DINNING", "STUDY ROOM", "TOILET"}
RED = {("F016", "F055"): 66, ("F018", "F052"): 68, ("F024", "F102"): 130, ("F086", "F259"): 110}       # kept / same-type deviations (mm)
EXTRA = {("F073", "F097"): 4875, ("F096", "F162"): 4875, ("F052", "F230"): 2600, ("F089", "F241"): 2250, ("F063", "F247"): 2300, ("F087", "F242"): 2300, ("F213", "F260"): 1500,
         ("F086", "F259"): 7070}      # CAD dimension strings; flat-4 hall 7070 is by symmetry with the flat-3 hall (no string found near it)
DROP = {("F102", "F244")}              # duplicate of the flat-3 hall 7070 reading


def build(root, seed=11, n_blue=6):
    O = os.path.join(root, "output"); g = json.load(open(f"{O}/floorplan_geometry.json")); rooms = [(Polygon(q["polygon"]), q["label"]) for q in g["rooms"]]
    real = {(p["a"], p["b"]): p for p in pairs([dict(id=f["id"], p0=f["p0"], p1=f["p1"], normal=f["normal"], sigma_m=f["sigma_m"]) for f in g["faces"]])}
    cad = {}
    for x in csv.DictReader(open(f"{O}/real_cad_distance_btw_walls.csv")): cad[(x["face_a"], x["face_b"])] = float(x["cad_distance_mm"])
    T = []
    for k, v in cad.items():
        if k in DROP or k not in real: continue
        mid = (real[k]["pa"] + real[k]["pb"]) / 2; room = next((l for pg, l in rooms if pg.buffer(0.1).contains(Point(mid))), None)
        if room in ROOMS_OK or k in RED: T.append(dict(a=k[0], b=k[1], cad=v, room=room, kind="existing"))
    for k, v in EXTRA.items():
        if k in real and k not in {(t["a"], t["b"]) for t in T}:
            mid = (real[k]["pa"] + real[k]["pb"]) / 2; room = next((l for pg, l in rooms if pg.buffer(0.1).contains(Point(mid))), None); T.append(dict(a=k[0], b=k[1], cad=float(v), room=room, kind="added"))
    for t in T:
        if (t["a"], t["b"]) in EXTRA: t["cad"] = float(EXTRA[(t["a"], t["b"])])
    rng = np.random.default_rng(seed); free = [i for i, t in enumerate(T) if (t["a"], t["b"]) not in RED]; blue = set(rng.choice(free, n_blue, replace=False).tolist())
    for i, t in enumerate(T):
        k = (t["a"], t["b"]); t["lidar"] = real[k]["dist"] * 1000; t["pa"] = real[k]["pa"].tolist(); t["pb"] = real[k]["pb"].tolist()
        if k in RED: t["klass"] = "red"; t["delta"] = float(RED[k])
        elif i in blue: t["klass"] = "blue"; t["delta"] = float(rng.choice([-1, 1]) * rng.uniform(8, 13))
        else: t["klass"] = "green"; t["delta"] = float(rng.choice([-1, 1]) * rng.uniform(1, 6))
    return T


def solve_shifts(T, fs0, lam=1e-3):
    """x_a + x_b = (lidar - cad - delta)/1000 for every target pair (distance = lidar - (x_a + x_b)); ridge toward the current shifts."""
    ids = sorted({t["a"] for t in T} | {t["b"] for t in T}); ix = {f: i for i, f in enumerate(ids)}
    A = np.zeros((len(T), len(ids))); b = np.zeros(len(T))
    for r, t in enumerate(T): A[r, ix[t["a"]]] = 1; A[r, ix[t["b"]]] = 1; b[r] = (t["lidar"] - t["cad"] - t["delta"]) / 1000
    x0 = np.array([fs0.get(f, 0.0) for f in ids]); x = np.linalg.solve(A.T @ A + lam * np.eye(len(ids)), A.T @ b + lam * x0)
    fs = dict(fs0); fs.update({f: float(x[ix[f]]) for f in ids}); res = (A @ x - b) * 1000
    return fs, res


if __name__ == "__main__":
    root = os.path.dirname(os.path.abspath(__file__)); out = os.path.join(root, "..", "poc_demo_files_output", "simulated_survey_grade")
    T = build(root); fs0 = json.load(open(f"{out}/SIMULATED_face_shifts_m.json")); fs, res = solve_shifts(T, fs0)
    json.dump(T, open(f"{out}/SIMULATED_target_readings.json", "w"), indent=1); json.dump(fs, open(f"{out}/SIMULATED_face_shifts_override.json", "w"))
    from collections import Counter
    print(len(T), "target readings:", dict(Counter(t["klass"] for t in T)), "| added", sum(t["kind"] == "added" for t in T), "| rooms", dict(Counter(t["room"] for t in T)))
    print("analytic residual of the linear system: max %.2f mm; max face shift %.0f mm" % (np.abs(res).max(), 1000 * max(abs(v) for v in fs.values())))
