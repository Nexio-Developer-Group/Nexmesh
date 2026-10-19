"""Flatten the floor-plan geometry into a measurement table (CSV) with source / method / uncertainty / status per feature."""
import csv
import numpy as np


def write_tables(cfg, P, g):
    rows = []
    for f in g["faces"]:
        L = float(np.hypot(*(np.array(f["p1"]) - np.array(f["p0"]))))
        rows.append(dict(id=f["id"], type="wall_face", x0=f["p0"][0], y0=f["p0"][1], x1=f["p1"][0], y1=f["p1"][1], length_m=L, width_m="",
                         source="LiDAR (registered, persistent returns)", method="Hough peak + TLS line fit, %d pts / %d scans, rms %.3f m" % (f["n_points"], f["n_scans"], f["rms"]),
                         sigma_m=f["sigma_m"], status="observed"))
    for o in g["openings"]:
        rows.append(dict(id=o["id"], type="opening", x0=o["p0"][0], y0=o["p0"][1], x1=o["p1"][0], y1=o["p1"][1], length_m=o["width"], width_m=o["width"],
                         source="LiDAR (gap between collinear faces %s)" % ",".join(o["faces"]), method="gap between face endpoints" + ("; model door nearby" if o["model_door_nearby"] else "; no model door nearby"),
                         sigma_m=o["sigma_m"], status="observed"))
    for m in g["model_derived_unobserved"]:
        L = float(np.hypot(*(np.array(m["p1"]) - np.array(m["p0"]))))
        rows.append(dict(id="M%s" % m["source"], type="wall_model_%s" % m["kind"].lower(), x0=m["p0"][0], y0=m["p0"][1], x1=m["p1"][0], y1=m["p1"][1], length_m=L, width_m="",
                         source="Blender model section", method="no LiDAR support within 8 cm", sigma_m="", status="model_derived"))
    for r in g["rooms"]:
        poly = np.array(r["polygon"]); c = poly.mean(0)
        a, b = r["min_rect_m"]
        rows.append(dict(id=r["id"], type=("corridor" if b / max(a, 1e-6) >= 3.5 else "room"), x0=c[0], y0=c[1], x1="", y1="", length_m=b, width_m=a,
                         source="free space bounded by %s faces (%.0f%% of perimeter LiDAR-observed)" % (r["status"], 100 * r["observed_perimeter_frac"]),
                         method="min-area rotated rectangle of free-space polygon; area %.2f m2; label=%s; nominal=%s" % (r["area_m2"], r["label"], r["nominal_mm"]),
                         sigma_m=r["sigma_m"], status=r["status"]))
    cols = ["id", "type", "x0", "y0", "x1", "y1", "length_m", "width_m", "units", "source", "method", "sigma_m", "status"]
    with open(P(cfg, "measurements.csv"), "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=cols); w.writeheader()
        for r in rows:
            r["units"] = "m"
            for k in ("x0", "y0", "x1", "y1", "length_m", "width_m", "sigma_m"):
                if isinstance(r.get(k), float):
                    r[k] = "%.4f" % r[k]
            w.writerow({c: r.get(c, "") for c in cols})
    return rows
