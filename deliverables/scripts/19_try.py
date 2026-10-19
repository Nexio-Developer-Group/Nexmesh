"""Try an absolute camera pose on a frame and show the overlay. usage: 19_try.py frame.jpg out.jpg x y z yaw_deg pitch_deg [x y z yaw pitch ...]
yaw 0 = looking +x (east), 90 = +y (north); pitch up positive; roll 0. Use poses_ref.json intrinsics."""
import sys, os, json, cv2, numpy as np
sys.path.insert(0, os.path.dirname(__file__)); from common import *
mesh, d = load_model('work/model.npz'); ren = Renderer(mesh, d['OBJ']); Pp = json.load(open('work/poses_pw.json')); q = Pp['f_01545.jpg']['params']; K = np.array([[q[0], 0, q[2]], [0, q[1], q[3]], [0, 0, 1.]])
def pose(x, y, z, yaw, pitch):
    y_, p_ = np.radians(yaw), np.radians(pitch); f = np.array([np.cos(p_) * np.cos(y_), np.cos(p_) * np.sin(y_), np.sin(p_)]); rt = np.cross(f, [0, 0, 1.]); rt /= np.linalg.norm(rt); return np.stack([rt, np.cross(f, rt), f], 1), np.array([x, y, z])
n = sys.argv[1]; und = cv2.undistort(cv2.imread('frames_all/' + n), K, np.array(q[4:8]), None, K); tiles = []
a = list(map(float, sys.argv[3:]))
for i in range(0, len(a), 5):
    R, C = pose(*a[i:i + 5]); dep, tri = ren.render(K, 1920, 1080, R, C, scale=0.5); e = edges_from(tri, d['OBJ'])
    e = cv2.dilate(cv2.resize(e.astype(np.uint8) * 255, (1920, 1080), interpolation=cv2.INTER_NEAREST), np.ones((3, 3), np.uint8)); ov = und.copy(); ov[e > 0] = (0, 0, 255)
    t = cv2.resize(ov, (640, 360)); cv2.putText(t, 'pose ' + ' '.join('%g' % v for v in a[i:i + 5]) + f' hit={np.isfinite(dep).mean():.2f}', (6, 20), 0, .5, (0, 255, 255), 2); tiles.append(t)
rows = [np.hstack(tiles[j:j + 2] + [np.zeros_like(tiles[0])] * (2 - len(tiles[j:j + 2]))) for j in range(0, len(tiles), 2)]
cv2.imwrite(sys.argv[2], np.vstack([np.hstack([cv2.resize(und, (640, 360)), np.zeros((360, 640, 3), np.uint8)])] + rows))
