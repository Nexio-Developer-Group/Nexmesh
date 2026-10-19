"""Overlay model edges on undistorted frames. usage: 05_verify.py sparse_dir sim3.json out.jpg idx1,idx2,..."""
import sys, json, re, cv2, numpy as np, pycolmap
sys.path.insert(0, __import__('os').path.dirname(__file__)); from common import *
rec = pycolmap.Reconstruction(sys.argv[1]); S = json.load(open(sys.argv[2])); s, R, t = S['s'], np.array(S['R']), np.array(S['t'])
mesh, d = load_model('work/model.npz'); ren = Renderer(mesh, d['OBJ']); cams = recon_cams(rec)
names = sorted(cams, key=lambda n: int(re.findall(r'\d+', n)[0])); pick = [int(i) for i in sys.argv[4].split(',')]
tiles = []
for i in pick:
    n = names[min(i, len(names) - 1)] if i < 10000 else n
    R_cw, t_cw, cam = cams[n]; Rc2w, C = sim3_apply_pose(s, R, t, R_cw, t_cw); K = K_of(cam)
    img = cv2.imread('frames_all/' + n); p = cam.params
    # undistort
    Kc = K.astype(np.float64); dist = np.array([p[4], p[5], p[6], p[7]])
    und = cv2.undistort(img, Kc, dist, None, Kc)
    depth, tri = ren.render(K, 1920, 1080, Rc2w, C, scale=0.5)
    e = edges_from(tri, d['OBJ']); e = cv2.resize(e.astype(np.uint8) * 255, (1920, 1080), interpolation=cv2.INTER_NEAREST)
    e = cv2.dilate(e, np.ones((3, 3), np.uint8))
    ov = und.copy(); ov[e > 0] = (0, 0, 255)
    sbs = np.hstack([cv2.resize(und, (640, 360)), cv2.resize(ov, (640, 360))]); cv2.putText(sbs, n, (8, 24), 0, .7, (0, 255, 255), 2); tiles.append(sbs)
cv2.imwrite(sys.argv[3], np.vstack(tiles))
