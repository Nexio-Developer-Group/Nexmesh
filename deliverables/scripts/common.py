import numpy as np, json, trimesh
from scipy.spatial.transform import Rotation as Rot
FLIP = np.diag([1., -1., -1.])      # OpenCV cam axes <-> Blender cam axes
def load_model(npz):
    d = np.load(npz); V, F = d['V'], d['F']
    return trimesh.Trimesh(V, F, process=False), d
def recon_cams(rec):
    """-> dict name -> (R_cw, t_cw, camera) in COLMAP frame"""
    out = {}
    for im in rec.images.values():
        p = im.cam_from_world() if callable(getattr(im, 'cam_from_world', None)) else im.cam_from_world
        T = p.matrix()            # 3x4
        out[im.name] = (T[:, :3].copy(), T[:, 3].copy(), rec.cameras[im.camera_id])
    return out
def sim3_apply_pose(s, R, t, R_cw, t_cw):
    """COLMAP world->cam pose to new world: X_b = s R X_c + t. returns (R_cw', t_cw') with scale s absorbed (cam->world rot, center)"""
    C = -R_cw.T @ t_cw
    Cb = s * R @ C + t
    Rc2w_b = R @ R_cw.T            # cam(opencv)->world(blender)
    return Rc2w_b, Cb

from trimesh.ray.ray_pyembree import RayMeshIntersector
class Renderer:
    """Embree ray-caster: depth (along optical axis), triangle id, object id for a pinhole camera (opencv axes)."""
    def __init__(self, mesh, obj_of_face):
        self.mesh = mesh; self.ray = RayMeshIntersector(mesh); self.obj = obj_of_face
        self.n = mesh.face_normals; self.p0 = mesh.vertices[mesh.faces[:, 0]]
    def render(self, K, W, H, Rc2w, C, scale=0.25):
        w, h = int(W * scale), int(H * scale)
        u, v = (np.arange(w) + .5) / scale, (np.arange(h) + .5) / scale
        uu, vv = np.meshgrid(u, v)
        d_cam = np.stack([(uu - K[0, 2]) / K[0, 0], (vv - K[1, 2]) / K[1, 1], np.ones_like(uu)], -1).reshape(-1, 3)
        d = d_cam @ Rc2w.T; d /= np.linalg.norm(d, axis=1, keepdims=True)
        o = np.tile(C, (len(d), 1)); tri = self.ray.intersects_first(o, d)
        hit = tri >= 0; depth = np.full(len(d), np.inf)
        t = np.einsum('ij,ij->i', self.p0[tri[hit]] - C, self.n[tri[hit]]) / np.einsum('ij,ij->i', d[hit], self.n[tri[hit]])
        zc = (d[hit] @ Rc2w)[:, 2] * t           # depth along optical axis
        depth[hit] = zc
        return depth.reshape(h, w), tri.reshape(h, w)
def edges_from(tri, obj):
    o = np.where(tri >= 0, obj[np.maximum(tri, 0)], -1)
    e = np.zeros(o.shape, bool)
    e[:, 1:] |= o[:, 1:] != o[:, :-1]; e[1:, :] |= o[1:, :] != o[:-1, :]
    return e
def K_of(cam, W=None, H=None):
    p = cam.params; return np.array([[p[0], 0, p[2]], [0, p[1], p[3]], [0, 0, 1.]])
