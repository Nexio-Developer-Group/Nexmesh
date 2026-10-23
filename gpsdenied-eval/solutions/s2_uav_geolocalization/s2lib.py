"""Shared helpers for S2 (map metres, tile cutting, query-crop). Not part of upstream code."""
import os, sys, json, math
import numpy as np, cv2
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, '..', '..'))
sys.path.insert(0, os.path.join(ROOT, 'harness'))
from geo import MapRef

MAP_PNG = os.path.join(ROOT, 'data/nidar/map_esri_z19.png')
MAP_JSON = os.path.join(ROOT, 'data/nidar/map_esri_z19.json')
GSD200 = 0.1804201549021668      # m/px at 200 m AGL (camera.json)
FRAME_W, FRAME_H = 1280, 800
TILE_PX = 256
_map_cache = {}

def load_map():
    if 'img' not in _map_cache:
        _map_cache['img'] = cv2.imread(MAP_PNG, cv2.IMREAD_GRAYSCALE)
        _map_cache['ref'] = MapRef(MAP_JSON)
        _map_cache['mpp'] = _map_cache['ref'].ground_m_per_px(18.52)
    return _map_cache['img'], _map_cache['ref'], _map_cache['mpp']

def cut_tile(img, cx_px, cy_px, tile_m, mpp, out=TILE_PX):
    """Axis-aligned (north-up) tile centred on map pixel, side tile_m metres, resized to out x out."""
    half = tile_m / mpp / 2.0
    M = np.array([[out / (2 * half), 0, -(cx_px - half) * out / (2 * half)],
                  [0, out / (2 * half), -(cy_px - half) * out / (2 * half)]], np.float32)
    return cv2.warpAffine(img, M, (out, out), flags=cv2.INTER_AREA if out < 2 * half else cv2.INTER_LINEAR,
                          borderMode=cv2.BORDER_CONSTANT, borderValue=0)

def query_crop(frame_gray, yaw_deg, alt_m, tile_m, out=TILE_PX, sign=-1.0):
    """North-up central crop of the camera frame: rotate by compass yaw, scale by altitude, crop tile_m square.
    Pixels outside the frame are black (CAEVL trains with masking so black corners are in-distribution)."""
    gsd = GSD200 * alt_m / 200.0
    half_px = tile_m / gsd / 2.0
    cx, cy = FRAME_W / 2.0, FRAME_H / 2.0
    # map output pixel (u,v) in north-up crop -> source pixel; build forward affine src->dst then invert via warpAffine flags
    s = out / (2 * half_px)
    a = math.radians(sign * yaw_deg)
    R = np.array([[math.cos(a), -math.sin(a)], [math.sin(a), math.cos(a)]])
    # dst = s * R @ (src - c) + out/2   (cv2 getRotationMatrix2D convention: positive angle = CCW in image coords)
    A = s * R
    t = np.array([out / 2.0, out / 2.0]) - A @ np.array([cx, cy])
    M = np.hstack([A, t[:, None]]).astype(np.float32)
    return cv2.warpAffine(frame_gray, M, (out, out), flags=cv2.INTER_AREA if s < 1 else cv2.INTER_LINEAR,
                          borderMode=cv2.BORDER_CONSTANT, borderValue=0)

def grid_tiles(img_shape, tile_m, stride_m, mpp, margin_frac=0.5):
    h, w = img_shape
    half = tile_m / mpp / 2.0
    step = stride_m / mpp
    xs = np.arange(half, w - half + 1, step); ys = np.arange(half, h - half + 1, step)
    return [(x, y) for y in ys for x in xs]

F_PX = (FRAME_W / 2) / math.tan(math.radians(30.0))   # 1108.5125 px (camera.json)

def rot_body_to_ned(roll, pitch, yaw):
    """Same convention as tools/render_video.py: R = Rz(yaw) Ry(pitch) Rx(roll); body x fwd, y right, z down."""
    cr, sr, cp, sp, cy, sy = math.cos(roll), math.sin(roll), math.cos(pitch), math.sin(pitch), math.cos(yaw), math.sin(yaw)
    Rz = np.array([[cy, -sy, 0], [sy, cy, 0], [0, 0, 1]]); Ry = np.array([[cp, 0, sp], [0, 1, 0], [-sp, 0, cp]])
    Rx = np.array([[1, 0, 0], [0, cr, -sr], [0, sr, cr]])
    return Rz @ Ry @ Rx

def principal_ground_offset(roll, pitch, yaw, alt):
    """(north, east) metres from the vehicle nadir point to the ground point the camera centre ray hits."""
    R = rot_body_to_ned(roll, pitch, yaw)
    d = R @ np.array([0.0, 0.0, 1.0])
    s = alt / d[2]
    return s * d[0], s * d[1]

def query_crop_att(frame_gray, roll, pitch, yaw, alt, tile_m, out=TILE_PX):
    """Attitude-rectified north-up crop (ground plane z=alt below the vehicle), centred on the ground point
    hit by the optical axis.  Returns (crop, (north_off, east_off)): vehicle = crop-centre position - offset.
    Pixels outside the frame are black.  This mirrors the author's make_queries_oriented.py idea (ground-plane
    resampling through the full attitude) but for this camera model."""
    R = rot_body_to_ned(roll, pitch, yaw)
    n0, e0 = principal_ground_offset(roll, pitch, yaw, alt)
    g = (np.arange(out) + 0.5) / out * tile_m - tile_m / 2          # metres across the tile
    east = e0 + g[None, :].repeat(out, 0)                            # columns -> east
    north = n0 - g[:, None].repeat(out, 1)                           # rows -> south
    d = np.stack([north, east, np.full_like(north, alt)], -1)       # NED vector vehicle->ground point
    b = d @ R                                                        # R^T d  (row-vector form)
    u = FRAME_W / 2 + F_PX * b[..., 1] / b[..., 2]
    v = FRAME_H / 2 - F_PX * b[..., 0] / b[..., 2]
    ok = (b[..., 2] > 0)
    u = np.where(ok, u, -1).astype(np.float32); v = np.where(ok, v, -1).astype(np.float32)
    # mild pre-blur when downsampling (frame GSD 0.18 m/px vs tile ~0.5 m/px)
    src = frame_gray
    ds = (tile_m / out) / (GSD200 * alt / 200.0)
    if ds > 1.5:
        k = ds * 0.5
        src = cv2.GaussianBlur(frame_gray, (0, 0), k)
    return cv2.remap(src, u, v, cv2.INTER_LINEAR, borderMode=cv2.BORDER_CONSTANT, borderValue=0), (n0, e0)
