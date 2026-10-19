"""Overlay model edges using poses json (refined or unrefined). usage: 05b_verify_poses.py poses.json out.jpg unrefined|refined name1,name2.."""
import sys, json, cv2, numpy as np
sys.path.insert(0, __import__('os').path.dirname(__file__)); from common import *
P = json.load(open(sys.argv[1])); mode = sys.argv[3]; mesh, d = load_model('work/model.npz'); ren = Renderer(mesh, d['OBJ']); tiles = []
for n in sys.argv[4].split(','):
    p = P[n]; Rc2w = np.array(p['R_c2w_unrefined' if mode == 'unrefined' else 'R_c2w']); C = np.array(p['C_unrefined' if mode == 'unrefined' else 'C'])
    q = p['params']; K = np.array([[q[0], 0, q[2]], [0, q[1], q[3]], [0, 0, 1.]])
    img = cv2.imread('frames_all/' + n); und = cv2.undistort(img, K, np.array(q[4:8]), None, K)
    depth, tri = ren.render(K, 1920, 1080, Rc2w, C, scale=0.5); e = edges_from(tri, d['OBJ'])
    e = cv2.dilate(cv2.resize(e.astype(np.uint8) * 255, (1920, 1080), interpolation=cv2.INTER_NEAREST), np.ones((3, 3), np.uint8))
    ov = und.copy(); ov[e > 0] = (0, 0, 255); sbs = np.hstack([cv2.resize(und, (480, 270)), cv2.resize(ov, (480, 270))]); cv2.putText(sbs, n + ' ' + mode, (8, 24), 0, .6, (0, 255, 255), 2); tiles.append(sbs)
cv2.imwrite(sys.argv[2], np.vstack(tiles))
