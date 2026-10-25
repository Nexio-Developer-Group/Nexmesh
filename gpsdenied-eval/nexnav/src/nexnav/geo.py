"""Geodesy helpers and the reference map (GeoMap): a georeferenced image the engines localise against.

Supported map inputs
  * GeoTIFF (any CRS pyproj knows; needs `rasterio` + `pyproj`)  -> `GeoMap.from_geotiff`
  * PNG/JPG + JSON sidecar of type "webmercator_tiles" (z, tile_x0, tile_y0, width, height) -> `GeoMap.from_png_json`
  * PNG/JPG + JSON sidecar of type "affine_latlon" (lat_top, lon_left, lat_bottom, lon_right) -> same loader
"""
import json, math, os
import numpy as np

R_EARTH = 6371008.8
WGS_A = 6378137.0


def haversine_m(lat1, lon1, lat2, lon2):
    """Great-circle distance in metres (arrays allowed)."""
    p1, p2 = np.radians(lat1), np.radians(lat2)
    dphi, dl = p2 - p1, np.radians(np.asarray(lon2, float) - lon1)
    a = np.sin(dphi / 2) ** 2 + np.cos(p1) * np.cos(p2) * np.sin(dl / 2) ** 2
    return 2 * R_EARTH * np.arcsin(np.sqrt(a))


def enu_delta_m(lat0, lon0, lat1, lon1):
    """(north, east) metres from point 0 to point 1 (small distances)."""
    n = np.radians(np.asarray(lat1, float) - lat0) * WGS_A
    e = np.radians(np.asarray(lon1, float) - lon0) * WGS_A * math.cos(math.radians(lat0))
    return n, e


def offset_latlon(lat, lon, north_m, east_m):
    return lat + math.degrees(north_m / WGS_A), lon + math.degrees(east_m / (WGS_A * math.cos(math.radians(lat))))


class GeoMap:
    """Georeferenced map. `gray` is the (H, W) uint8 image used by the engines; `rgb` is kept for engines that need colour."""

    def __init__(self, image_bgr, to_px, to_latlon, name="map"):
        self.bgr = image_bgr
        self.height, self.width = image_bgr.shape[:2]
        self._to_px, self._to_ll = to_px, to_latlon
        self.name = name
        import cv2
        self.gray = cv2.cvtColor(image_bgr, cv2.COLOR_BGR2GRAY) if image_bgr.ndim == 3 else image_bgr

    # --- conversions (native map pixels) ---
    def latlon_to_px(self, lat, lon):
        return self._to_px(np.asarray(lat, float), np.asarray(lon, float))

    def px_to_latlon(self, px, py):
        return self._to_ll(np.asarray(px, float), np.asarray(py, float))

    def centre_latlon(self):
        la, lo = self.px_to_latlon(self.width / 2, self.height / 2)
        return float(la), float(lo)

    def ground_m_per_px(self):
        """Metres per pixel at the map centre, measured through the georeference (any CRS)."""
        cx, cy = self.width / 2, self.height / 2
        la0, lo0 = self.px_to_latlon(cx, cy); la1, lo1 = self.px_to_latlon(cx + 100.0, cy)
        return float(haversine_m(la0, lo0, la1, lo1)) / 100.0

    def contains(self, lat, lon, margin_px=0):
        x, y = self.latlon_to_px(lat, lon)
        return bool((margin_px <= x < self.width - margin_px) and (margin_px <= y < self.height - margin_px))

    def resampled(self, scale):
        """New GeoMap with the image scaled by `scale` (pixel size shrinks by 1/scale); georeference preserved."""
        import cv2
        if abs(scale - 1.0) < 1e-3: return self
        img = cv2.resize(self.bgr, None, fx=scale, fy=scale, interpolation=cv2.INTER_AREA if scale < 1 else cv2.INTER_CUBIC)
        return GeoMap(img, lambda la, lo: tuple(v * scale for v in self._to_px(la, lo)), lambda x, y: self._to_ll(x / scale, y / scale), self.name)

    # --- loaders ---
    @classmethod
    def load(cls, path, georef=None):
        ext = os.path.splitext(path)[1].lower()
        if ext in (".tif", ".tiff") and georef is None:
            return cls.from_geotiff(path)
        return cls.from_png_json(path, georef or (os.path.splitext(path)[0] + ".json"))

    @classmethod
    def from_png_json(cls, path, json_path):
        import cv2
        img = cv2.imread(path, cv2.IMREAD_COLOR)
        if img is None: raise FileNotFoundError(f"cannot read map image {path}")
        if not os.path.exists(json_path): raise FileNotFoundError(f"map georeference sidecar not found: {json_path}")
        m = json.load(open(json_path))
        if "z" in m and "tile_x0" in m:  # web-mercator tile mosaic (256 px tiles)
            z, x0, y0 = m["z"], m["tile_x0"], m["tile_y0"]; n = 2 ** z

            def to_px(lat, lon):
                x = (lon + 180) / 360 * n
                y = (1 - np.arcsinh(np.tan(np.radians(lat))) / np.pi) / 2 * n
                return (x - x0) * 256, (y - y0) * 256

            def to_ll(px, py):
                x, y = (px / 256 + x0) / n, (py / 256 + y0) / n
                return np.degrees(np.arctan(np.sinh(np.pi * (1 - 2 * y)))), x * 360 - 180
            return cls(img, to_px, to_ll, os.path.basename(path))
        if "lat_top" in m:  # simple equirectangular box
            H, W = img.shape[:2]
            la_t, lo_l, la_b, lo_r = m["lat_top"], m["lon_left"], m["lat_bottom"], m["lon_right"]
            to_px = lambda lat, lon: ((lon - lo_l) / (lo_r - lo_l) * W, (la_t - lat) / (la_t - la_b) * H)
            to_ll = lambda px, py: (la_t - py / H * (la_t - la_b), lo_l + px / W * (lo_r - lo_l))
            return cls(img, to_px, to_ll, os.path.basename(path))
        raise ValueError(f"unknown georeference sidecar format in {json_path}")

    @classmethod
    def from_geotiff(cls, path):
        try:
            import rasterio
            from pyproj import Transformer
        except ImportError as e:
            raise ImportError("GeoTIFF maps need `rasterio` and `pyproj` (pip install nexnav[geotiff])") from e
        import cv2
        with rasterio.open(path) as src:
            bands = src.read()
            if bands.shape[0] >= 3: img = np.dstack([bands[2], bands[1], bands[0]])
            else: img = bands[0]
            if img.dtype != np.uint8:
                lo, hi = np.percentile(img, [1, 99]); img = np.clip((img - lo) / max(hi - lo, 1) * 255, 0, 255).astype(np.uint8)
            if img.ndim == 2: img = cv2.cvtColor(img, cv2.COLOR_GRAY2BGR)
            tr, crs = src.transform, src.crs
        fwd = Transformer.from_crs("EPSG:4326", crs, always_xy=True); inv = Transformer.from_crs(crs, "EPSG:4326", always_xy=True)
        a, b, c, d, e, f = tr.a, tr.b, tr.c, tr.d, tr.e, tr.f  # x = a*col + b*row + c ; y = d*col + e*row + f

        def to_px(lat, lon):
            x, y = fwd.transform(lon, lat)
            det = a * e - b * d
            col = (e * (x - c) - b * (y - f)) / det; row = (-d * (x - c) + a * (y - f)) / det
            return col, row

        def to_ll(px, py):
            x = a * px + b * py + c; y = d * px + e * py + f
            lon, lat = inv.transform(x, y)
            return lat, lon
        return cls(img, to_px, to_ll, os.path.basename(path))
