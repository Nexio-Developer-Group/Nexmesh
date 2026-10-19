import numpy as np, cv2, json, sys
from shapely.geometry import Polygon, MultiPolygon, box
from shapely.ops import unary_union
poses = json.load(open(sys.argv[1])); RES = 0.05; lo = np.array([-21., -14.]); hi = np.array([18., 14.]); Wg, Hg = ((hi - lo) / RES).astype(int)
wall = cv2.imread('wall_mask.png', 0); px = lambda p: tuple(((np.asarray(p) - lo) / RES).round().astype(int))
def closed(r):
    k = cv2.getStructuringElement(cv2.MORPH_ELLIPSE, (int(2 * r / RES) | 1,) * 2); c = cv2.morphologyEx(wall, cv2.MORPH_CLOSE, k); ff = c.copy(); m = np.zeros((Hg + 2, Wg + 2), np.uint8); cv2.floodFill(ff, m, (0, 0), 128); return (ff != 128).astype(np.uint8)
P10, P15 = closed(1.0), closed(1.5)
path = np.zeros((Hg, Wg), np.uint8)
for p in poses.values():
    x, y = px(p['C'][:2]); cv2.circle(path, (x, y), int(2.5 / RES), 1, -1)
d = np.load('model_new.npz')['V']; bb = (d.min(0)[:2] - 0.2, d.max(0)[:2] + 0.2); bm = np.zeros((Hg, Wg), np.uint8); x0, y0 = px(bb[0]); x1, y1 = px(bb[1]); bm[y0:y1, x0:x1] = 1
plate = ((P10 > 0) | ((path > 0) & (P15 > 0))) & (bm > 0)
plate = cv2.morphologyEx(plate.astype(np.uint8), cv2.MORPH_OPEN, np.ones((5, 5), np.uint8))          # drop slivers
# remove tiny holes / islands
n, lab, st, _ = cv2.connectedComponentsWithStats(plate, connectivity=8); keep = np.zeros_like(plate)
for i in range(1, n):
    if st[i, cv2.CC_STAT_AREA] * RES * RES > 3.0: keep[lab == i] = 1
inv = (1 - keep).astype(np.uint8); n2, lab2, st2, _ = cv2.connectedComponentsWithStats(inv, connectivity=4)
for i in range(1, n2):
    if st2[i, cv2.CC_STAT_AREA] * RES * RES < 1.0: keep[lab2 == i] = 1                                 # fill holes < 1 m2
np.save('plate_final.npy', keep); print("plate area %.0f m2" % (keep.sum() * RES * RES))
cnts, hier = cv2.findContours(keep, cv2.RETR_CCOMP, cv2.CHAIN_APPROX_SIMPLE); polys = []
w2 = lambda c: [(lo[0] + (p[0][0] + .5) * RES, lo[1] + (p[0][1] + .5) * RES) for p in c]
for i, c in enumerate(cnts):
    if hier[0][i][3] == -1 and len(c) >= 4:
        holes = [w2(cnts[j]) for j in range(len(cnts)) if hier[0][j][3] == i and len(cnts[j]) >= 4]; pg = Polygon(w2(c), holes).buffer(0)
        if pg.area > 3: polys.append(pg.simplify(0.04))
mp = unary_union(polys); print("polygons", len(polys), "area %.0f" % mp.area)
json.dump([{'ext': list(map(list, g.exterior.coords)), 'holes': [list(map(list, h.coords)) for h in g.interiors]} for g in (mp.geoms if isinstance(mp, MultiPolygon) else [mp])], open('plate_polys.json', 'w'))
im = cv2.cvtColor(keep * 255, cv2.COLOR_GRAY2BGR); im[wall > 0] = (0, 0, 0); im = cv2.flip(im, 0); cv2.imwrite('plate_final.jpg', cv2.resize(im, (1170, 840)))
