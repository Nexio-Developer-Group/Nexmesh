"""Render the floor plan to SVG / PDF / PNG (matplotlib, one renderer so they agree) and DXF (ezdxf)."""
import numpy as np
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import Polygon as MPoly
import ezdxf

STATUS_FILL = {"observed": "#cfe5f7", "partially_observed": "#fdf0b8", "model_derived": "#e6e6e6"}


def _fmt(v):
    return "%.2f" % v      # 10 mm resolution: measurement sigma is ~2-4 cm


def export_all(cfg, P, g, pts):
    plt.rcParams["svg.fonttype"] = "none"
    fig, ax = plt.subplots(figsize=(16.5, 11.7))
    allxy = np.array([f["p0"] for f in g["faces"]] + [f["p1"] for f in g["faces"]])
    for r in g["rooms"]:
        poly = np.array(r["polygon"])
        ax.add_patch(MPoly(poly, closed=True, fc=STATUS_FILL[r["status"]], ec="none", alpha=0.9, hatch="//" if r["status"] == "model_derived" else None, zorder=1))
    for m in g["model_derived_unobserved"]:
        ax.plot([m["p0"][0], m["p1"][0]], [m["p0"][1], m["p1"][1]], color="#8a8a8a", lw=0.9, ls=(0, (4, 2)), zorder=2)
    for f in g["faces"]:
        ax.plot([f["p0"][0], f["p1"][0]], [f["p0"][1], f["p1"][1]], color="#111", lw=1.1, solid_capstyle="butt", zorder=3)
        L = np.hypot(*(np.array(f["p1"]) - np.array(f["p0"])))
        if L >= 2.5:
            mid = (np.array(f["p0"]) + np.array(f["p1"])) / 2; n = np.array(f["normal"]); ang = np.degrees(np.arctan2(f["p1"][1] - f["p0"][1], f["p1"][0] - f["p0"][0]))
            ang = ang if -90 < ang <= 90 else ang + 180
            ax.text(*(mid + n * 0.18), _fmt(L), fontsize=4.2, ha="center", va="center", rotation=ang, color="#444", zorder=5)
    for o in g["openings"]:
        ax.plot([o["p0"][0], o["p1"][0]], [o["p0"][1], o["p1"][1]], color="#e07b00", lw=2.2, zorder=4, solid_capstyle="butt")
    for r in g["rooms"]:
        poly = np.array(r["polygon"]); c = poly.mean(0); a, b = r["min_rect_m"]
        txt = ("%s\n" % r["label"] if r["label"] else "") + "%s × %s m\n%.1f m²" % (_fmt(b), _fmt(a), r["area_m2"])
        ax.text(c[0], c[1], txt, fontsize=5.0, ha="center", va="center", zorder=6, color="#102a43" if r["status"] != "model_derived" else "#666")
    sph = np.array(g.get("start_xy", [np.nan, np.nan]))
    if np.isfinite(sph).all():
        ax.plot(*sph, "o", color="#d00", ms=7, zorder=7); ax.text(sph[0] + 0.3, sph[1] + 0.3, "start (sphere)", fontsize=6, color="#d00")
    lo, hi = pts.min(0) - 1.5, pts.max(0) + 1.5
    ax.set_xlim(lo[0], hi[0]); ax.set_ylim(lo[1], hi[1]); ax.set_aspect("equal")
    ax.set_xticks(np.arange(np.floor(lo[0] / 5) * 5, hi[0], 5)); ax.set_yticks(np.arange(np.floor(lo[1] / 5) * 5, hi[1], 5))
    ax.set_xticks(np.arange(np.floor(lo[0]), hi[0], 1), minor=True); ax.set_yticks(np.arange(np.floor(lo[1]), hi[1], 1), minor=True)
    ax.grid(True, which="major", color="#bbb", lw=0.5); ax.grid(True, which="minor", color="#eee", lw=0.3)
    ax.set_xlabel("X (m)  — Blender world frame"); ax.set_ylabel("Y (m)")
    # scale bar, north arrow
    x0, y0 = hi[0] - 9, lo[1] + 1.0
    ax.plot([x0, x0 + 5], [y0, y0], "k-", lw=3); ax.text(x0 + 2.5, y0 + 0.3, "5 m", ha="center", fontsize=7)
    ax.annotate("", xy=(hi[0] - 1.5, lo[1] + 4.5), xytext=(hi[0] - 1.5, lo[1] + 2.0), arrowprops=dict(arrowstyle="-|>", lw=1.5)); ax.text(hi[0] - 1.5, lo[1] + 4.9, "+Y\n(model ref. dir.)", ha="center", fontsize=6)
    from matplotlib.lines import Line2D
    from matplotlib.patches import Patch
    ax.legend(handles=[Line2D([], [], color="#111", lw=1.2, label="Wall face — LiDAR-observed (line fit)"), Line2D([], [], color="#8a8a8a", lw=1, ls="--", label="Wall — model-derived (no LiDAR support)"),
                       Line2D([], [], color="#e07b00", lw=2.2, label="Opening — LiDAR-observed gap"), Patch(fc=STATUS_FILL["observed"], label="Room: ≥70% perimeter observed"),
                       Patch(fc=STATUS_FILL["partially_observed"], label="Room: 30–70% observed"), Patch(fc=STATUS_FILL["model_derived"], hatch="//", label="Room: <30% observed (model-derived)")],
              loc="lower left", fontsize=6, framealpha=0.95)
    ax.set_title("Floor 18 Typical Floor, Wing B — LiDAR-derived floor plan (dimensions in m, ±%.0f–%.0f mm for observed faces)" % (1000 * 0.02, 1000 * 0.04), fontsize=9)
    for ext in ("svg", "pdf", "png"):
        fig.savefig(P(cfg, "floorplan." + ext), dpi=200 if ext == "png" else None, bbox_inches="tight")
    plt.close(fig)
    export_dxf(P(cfg, "floorplan.dxf"), g)


def export_dxf(path, g):
    doc = ezdxf.new("R2018", setup=True); doc.units = ezdxf.units.M; msp = doc.modelspace()
    for name, col, lt in (("A-WALL-OBSERVED", 7, "CONTINUOUS"), ("A-WALL-MODEL-DERIVED", 8, "DASHED"), ("A-OPENING-OBSERVED", 30, "CONTINUOUS"), ("A-ROOM", 5, "CONTINUOUS"),
                          ("A-ANNO-DIM", 2, "CONTINUOUS"), ("A-ANNO-TEXT", 3, "CONTINUOUS")):
        doc.layers.add(name, color=col, linetype=lt)
    for f in g["faces"]:
        msp.add_line(f["p0"], f["p1"], dxfattribs={"layer": "A-WALL-OBSERVED"})
        L = np.hypot(*(np.array(f["p1"]) - np.array(f["p0"])))
        if L >= 2.5:
            mid = (np.array(f["p0"]) + np.array(f["p1"])) / 2 + np.array(f["normal"]) * 0.18
            ang = np.degrees(np.arctan2(f["p1"][1] - f["p0"][1], f["p1"][0] - f["p0"][0])); ang = ang if -90 < ang <= 90 else ang + 180
            msp.add_text(_fmt(L), height=0.09, rotation=ang, dxfattribs={"layer": "A-ANNO-DIM"}).set_placement(tuple(mid), align=ezdxf.enums.TextEntityAlignment.MIDDLE_CENTER)
    for m in g["model_derived_unobserved"]:
        msp.add_line(m["p0"], m["p1"], dxfattribs={"layer": "A-WALL-MODEL-DERIVED"})
    for o in g["openings"]:
        msp.add_line(o["p0"], o["p1"], dxfattribs={"layer": "A-OPENING-OBSERVED", "lineweight": 50})
    for r in g["rooms"]:
        msp.add_lwpolyline(r["polygon"], close=True, dxfattribs={"layer": "A-ROOM"})
        c = np.array(r["polygon"]).mean(0); a, b = r["min_rect_m"]
        msp.add_text("%s %s x %s m (%s)" % (r["label"] or r["id"], _fmt(b), _fmt(a), r["status"]), height=0.12, dxfattribs={"layer": "A-ANNO-TEXT"}).set_placement(tuple(c), align=ezdxf.enums.TextEntityAlignment.MIDDLE_CENTER)
    doc.saveas(path)
