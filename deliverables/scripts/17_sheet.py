"""Overlay contact sheet. usage: 17_sheet.py poses.json out.jpg frame_idx1,frame_idx2,... (plain 3fps indices; nearest registered used)"""
import sys, os, re, json, cv2, numpy as np
sys.path.insert(0, os.path.dirname(__file__)); from common import *
P = json.load(open(sys.argv[1])); mesh, d = load_model('work/model.npz'); ren = Renderer(mesh, d['OBJ'])
idx = lambda n: int(re.findall(r'\d+', n)[0]); pl = {idx(n): n for n in P if n.count('_') == 1}; tiles = []
for t in map(int, sys.argv[3].split(',')):
    n = pl[min(pl, key=lambda k: abs(k - t))]; p = P[n]; q = p['params']; K = np.array([[q[0], 0, q[2]], [0, q[1], q[3]], [0, 0, 1.]])
    und = cv2.undistort(cv2.imread('frames_all/' + n), K, np.array(q[4:8]), None, K)
    dep, tri = ren.render(K, 1920, 1080, np.array(p['R_c2w']), np.array(p['C']), scale=0.5); e = edges_from(tri, d['OBJ'])
    e = cv2.dilate(cv2.resize(e.astype(np.uint8) * 255, (1920, 1080), interpolation=cv2.INTER_NEAREST), np.ones((3, 3), np.uint8)); ov = und.copy(); ov[e > 0] = (0, 0, 255)
    sb = np.hstack([cv2.resize(und, (448, 252)), cv2.resize(ov, (448, 252))]); cv2.putText(sb, f"{n} anchor={p.get('anchor')} C=({p['C'][0]:.1f},{p['C'][1]:.1f},{p['C'][2]:.1f})", (6, 20), 0, .5, (0, 255, 255), 2); tiles.append(sb)
cv2.imwrite(sys.argv[2], np.vstack(tiles))
