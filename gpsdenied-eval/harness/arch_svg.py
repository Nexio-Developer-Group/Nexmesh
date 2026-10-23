"""Vector architecture diagrams (SVG) of the REAL data flow of each solution, drawn from the code that ran
(stages and libraries from each solution's ANALYSIS.md). Usage: python arch_svg.py <out_dir>"""
import os, sys
from xml.sax.saxutils import escape

# each stage: (title, subtitle[models/libs], kind) kind: in | model | geom | filter | out | ours
ARCH = {
 "s1": ("S1 koksalan/gnss-denied-nav (degraded: pretrained DINOv2-base)", [
   ("Camera frame", "gray->RGB, 1280x800", "in"), ("Compass yaw + baro height", "hints", "in"),
   ("North-up + scale warp", "OpenCV affine", "geom"), ("DINOv2-base embedding", "PyTorch / HF transformers", "model"),
   ("Top-10 tiles", "dot-product over tile DB (prebuilt)", "model"), ("SuperPoint + LightGlue", "keypoints, matching", "model"),
   ("RANSAC + planar PnP", "OpenCV: position, height, tilt", "geom"), ("Consistency gate", "height vs baro, tilt vs IMU", "filter"),
   ("Lucas-Kanade tracking", "tracking window after first lock", "filter"), ("Fix (lat, lon)", "-> GPS_INPUT bridge", "out")]),
 "s2": ("S2 uav-geolocalization (CAEVL + VIO; VIO = OUR substitute, not OpenVINS)", [
   ("Camera frame + yaw + baro", "north-up rotate/scale (our adaptation)", "in"), ("Canny edge map", "OpenCV 70/200", "geom"),
   ("CAEVL encoder 1024-d", "PyTorch CPU, our 50-min training", "model"), ("NN search over tile descriptors", "optional corridor around VIO prior", "model"),
   ("VIO substitute", "phase correlation + yaw + baro (OpenVINS not run)", "ours"), ("fuse.py consensus RANSAC", "30 s window, 60 m inlier radius", "filter"),
   ("Align / EKF / causal", "VIO trajectory aligned to accepted fixes", "filter"), ("Fused estimate", "-> GPS_INPUT bridge", "out")]),
 "s3": ("S3 ShuhaievaPolina tile-matching (notebook pipeline)", [
   ("Camera frame", "RGB, 1280x800", "in"), ("DINOv2-small (timm)", "518x518, L2-normalised", "model"),
   ("Top-5 tiles", "cosine vs 1024px tile DB (stride 512)", "model"), ("ALIKED 1024 kp", "extract (frame + tile)", "model"),
   ("LightGlue", "match, up to 3 candidates", "model"), ("USAC_MAGSAC homography", "OpenCV, >=6 inliers", "geom"),
   ("Project frame centre", "tile -> map px -> lat/lon", "geom"), ("Fix / tile-centre fallback", "fallback scored separately", "out")]),
 "s4": ("S4 hmakelin/gisnav (read from source; NOT RUN)", [
   ("Camera + autopilot state", "MAVROS prior pos/heading/alt", "in"), ("BBoxNode", "FOV footprint", "geom"),
   ("GISNode", "WMS GetMap (OWSLib) -> RGB + DEM", "model"), ("StereoNode", "gray, rotate by heading, crop", "geom"),
   ("PoseNode", "SIFT + LightGlue (kornia) + DEM + solvePnPRansac", "model"), ("TwistNode", "SIFT visual odometry", "geom"),
   ("robot_localization EKFs", "local + global, 5 Hz", "filter"), ("UORBNode", "micro-ROS -> PX4 fake GPS", "out")]),
 "s5": ("S5 SWA-PF (substitute semantic map; odometry from noisy telemetry)", [
   ("Camera frame", "BGR 1280x800", "in"), ("SegFormer-B0 (7 classes)", "PyTorch 1.10 CPU, shipped weights", "model"),
   ("Class images x100 yaws", "400x400, bowl weight, integral images", "geom"), ("Semantic map + distance maps", "offline; OUR substitute labels", "ours"),
   ("Particle weighting", "N x (x,y,alt,yaw), 160000/sum^2", "filter"), ("Motion update + resample", "odometry + Gaussian noise", "filter"),
   ("Spread check + DBSCAN", "cluster centre, >=50% particles", "filter"), ("Estimate (lat, lon)", "accepted when converged", "out")]),
 "s6": ("S6 our composite (MegaLoc + XFeat/LighterGlue + PnP)", [
   ("Camera frame + yaw + baro", "hints", "in"), ("Rectify to north-up canvas", "ground-plane homography, OpenCV", "geom"),
   ("MegaLoc (DINOv2-B + SALAD)", "8448-d, PyTorch", "model"), ("FAISS IndexFlatIP top-5", "625 tiles", "model"),
   ("XFeat + LighterGlue", "2048 kp, kornia", "model"), ("MAGSAC + planar PnP", "OpenCV IPPE + LM", "geom"),
   ("Consistency gate", ">=20 inliers, residual, height, yaw", "filter"), ("Tracking window", "constant-velocity prediction, skip retrieval", "filter"),
   ("Fix (lat, lon)", "-> GPS_INPUT bridge", "out")]),
}
FILL = {"in": "#e8eef9", "model": "#dff1e4", "geom": "#fff3d6", "filter": "#fde3e0", "out": "#d9e8ff", "ours": "#efe0f7"}


def svg(title, stages, per_row=4, w=210, h=64, gx=34, gy=34):
    rows = [stages[i:i + per_row] for i in range(0, len(stages), per_row)]
    W = per_row * w + (per_row - 1) * gx + 40; H = 70 + len(rows) * (h + gy)
    o = [f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" font-family="Helvetica,Arial,sans-serif">',
         '<defs><marker id="a" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#555"/></marker></defs>',
         f'<text x="20" y="28" font-size="15" font-weight="bold">{escape(title)}</text>']
    pos = []
    for r, row in enumerate(rows):
        for c, st in enumerate(row):
            x = 20 + c * (w + gx); y = 50 + r * (h + gy); pos.append((x, y))
            o.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="8" fill="{FILL[st[2]]}" stroke="#555"/>')
            o.append(f'<text x="{x + w / 2}" y="{y + 26}" font-size="12.5" font-weight="bold" text-anchor="middle">{escape(st[0])}</text>')
            sub = st[1]
            lines = [sub] if len(sub) <= 34 else [sub[:sub.rfind(" ", 0, 34)], sub[sub.rfind(" ", 0, 34) + 1:]]
            for k, ln in enumerate(lines):
                o.append(f'<text x="{x + w / 2}" y="{y + 44 + k * 13}" font-size="10.5" fill="#333" text-anchor="middle">{escape(ln)}</text>')
    for i in range(len(pos) - 1):
        (x1, y1), (x2, y2) = pos[i], pos[i + 1]
        if y1 == y2: o.append(f'<line x1="{x1 + w}" y1="{y1 + h / 2}" x2="{x2 - 2}" y2="{y2 + h / 2}" stroke="#555" marker-end="url(#a)"/>')
        else:
            ym = y1 + h + gy / 2
            o.append(f'<path d="M{x1 + w / 2},{y1 + h} L{x1 + w / 2},{ym} L{x2 + w / 2},{ym} L{x2 + w / 2},{y2 - 2}" fill="none" stroke="#555" marker-end="url(#a)"/>')
    lg = [("input/hint", "in"), ("learned model", "model"), ("geometry", "geom"), ("filter/gate", "filter"), ("output", "out"), ("OUR substitute", "ours")]
    for k, (n, kd) in enumerate(lg):
        x = 20 + k * 120; y = H - 22
        o.append(f'<rect x="{x}" y="{y - 10}" width="12" height="12" fill="{FILL[kd]}" stroke="#555"/><text x="{x + 16}" y="{y}" font-size="10">{n}</text>')
    o.append("</svg>")
    return "\n".join(o)


if __name__ == "__main__":
    out = sys.argv[1]; os.makedirs(out, exist_ok=True)
    for k, (t, st) in ARCH.items():
        open(os.path.join(out, f"{k}.svg"), "w").write(svg(t, st))
    print("wrote", len(ARCH), "diagrams")
