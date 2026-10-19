"""Export an SfM model + Sim3 as the poses json used by later scripts. usage: 04c_export_poses.py model sim3.json out.json"""
import sys, json, numpy as np, pycolmap
sys.path.insert(0, __import__('os').path.dirname(__file__)); from common import *
rec = pycolmap.Reconstruction(sys.argv[1]); S = json.load(open(sys.argv[2])); s, R, t = S['s'], np.array(S['R']), np.array(S['t']); out = {}
for n, (Rcw, tcw, cam) in recon_cams(rec).items():
    Rc, C = sim3_apply_pose(s, R, t, Rcw, tcw)
    out[n] = {'R_c2w': Rc.tolist(), 'C': C.tolist(), 'R_c2w_unrefined': Rc.tolist(), 'C_unrefined': C.tolist(), 'params': list(cam.params), 'W': cam.width, 'H': cam.height}
json.dump(out, open(sys.argv[3], 'w')); print(len(out))
