"""Top-down plan: walls grey, windows blue, doors orange; camera path with heading arrows + frame labels.
usage: 18_plan.py poses.json out.jpg idx_from idx_to label_step [xmin,xmax,ymin,ymax]"""
import sys, os, re, json, cv2, numpy as np
sys.path.insert(0, os.path.dirname(__file__)); from common import *
P = json.load(open(sys.argv[1])); a, b, step = int(sys.argv[3]), int(sys.argv[4]), int(sys.argv[5]); box = list(map(float, sys.argv[6].split(','))) if len(sys.argv) > 6 else [-20, 17, -13, 13]
mesh, d = load_model('work/model.npz'); mj = json.load(open('work/model.json')); names = [x[0] for x in mj['names']]; OBJ = d['OBJ']; V, F = mesh.vertices, mesh.faces
sc = 40; W, H = int((box[1] - box[0]) * sc), int((box[3] - box[2]) * sc); img = np.full((H, W, 3), 255, np.uint8)
pt = lambda x, y: (int((x - box[0]) * sc), int(H - (y - box[2]) * sc))
for fi, f in enumerate(F):
    nm = names[OBJ[fi]]; col = (255, 120, 0) if nm.startswith('Window') else (0, 140, 255) if nm.startswith('Door') else (150, 150, 150); p = V[f]
    if abs(np.cross(p[1] - p[0], p[2] - p[0])[2]) < 1e-6:
        for i in range(3): cv2.line(img, pt(*p[i][:2]), pt(*p[(i + 1) % 3][:2]), col, 2 if col != (150, 150, 150) else 1)
idx = lambda n: int(re.findall(r'\d+', n)[0]); pl = sorted([n for n in P if n.count('_') == 1 and a <= idx(n) <= b], key=idx); prev = None
for n in pl:
    C = np.array(P[n]['C']); R = np.array(P[n]['R_c2w']); f = R[:, 2][:2]; f = f / (np.linalg.norm(f) + 1e-9); c = pt(*C[:2]); col = (0, 0, 220) if P[n].get('anchor') else (0, 160, 0)
    if prev is not None: cv2.line(img, prev, c, col, 1)
    prev = c
    if idx(n) % step == 0:
        cv2.arrowedLine(img, c, (int(c[0] + f[0] * 28), int(c[1] - f[1] * 28)), col, 2, tipLength=.3); cv2.putText(img, str(idx(n)), (c[0] + 3, c[1] - 6), 0, .45, (0, 0, 0), 1)
cv2.imwrite(sys.argv[2], img); print(img.shape)
