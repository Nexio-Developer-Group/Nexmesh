"""Sim3 ext_old -> ext_new from shared cameras (Umeyama, RANSAC-ish trimmed), composed with ext_old -> Blender.
usage: 04b_compose_sim3.py old_model new_model sim3_old.json out.json"""
import sys, json, numpy as np, pycolmap
sys.path.insert(0, __import__('os').path.dirname(__file__)); from common import *
old = recon_cams(pycolmap.Reconstruction(sys.argv[1])); new = recon_cams(pycolmap.Reconstruction(sys.argv[2])); S = json.load(open(sys.argv[3]))
names = sorted(set(old) & set(new)); C = lambda d, n: -d[n][0].T @ d[n][1]
A = np.array([C(old, n) for n in names]); B = np.array([C(new, n) for n in names])
def umeyama(A, B):
    ma, mb = A.mean(0), B.mean(0); a, b = A - ma, B - mb; U, D, Vt = np.linalg.svd(b.T @ a / len(A)); Sg = np.eye(3); Sg[2, 2] = np.sign(np.linalg.det(U @ Vt))
    R = U @ Sg @ Vt; s = (D * np.diag(Sg)).sum() / (a ** 2).sum() * len(A); return s, R, mb - s * R @ ma
keep = np.ones(len(A), bool)
for it in range(5):
    s, R, t = umeyama(A[keep], B[keep]); res = np.linalg.norm(s * (R @ A.T).T + t - B, axis=1); keep = res < max(3 * np.median(res), 1e-3)
print("shared cams %d, kept %d, old->new scale %.4f, median resid %.4f (new units = %.3f m)" % (len(A), keep.sum(), s, np.median(res), np.median(res) * 1.74 / 1))
# composition: X_blender = s0 R0 X_old + t0 ; X_old = (1/s) R^T (X_new - t)
s0, R0, t0 = S['s'], np.array(S['R']), np.array(S['t']); s2 = s0 / s; R2 = R0 @ R.T; t2 = t0 - s2 * R2 @ t
json.dump({'s': float(s2), 'R': R2.tolist(), 't': t2.tolist()}, open(sys.argv[4], 'w')); print("composed scale", s2)
