"""Map georeference helpers for the Esri z19 mosaic (Web-Mercator, 256 px tiles)."""
import json, math
import numpy as np

ORIGIN = 20037508.342789244
EARTH = 6378137.0


class MapRef:
    def __init__(self, json_path):
        m = json.load(open(json_path))
        self.z, self.x0, self.y0 = m["z"], m["tile_x0"], m["tile_y0"]
        self.width, self.height = m["width"], m["height"]
        self.n = 2 ** self.z
        self.merc_res = 2 * ORIGIN / (256 * self.n)  # mercator metres per pixel

    def latlon_to_px(self, lat, lon):
        lat, lon = np.asarray(lat, float), np.asarray(lon, float)
        x = (lon + 180) / 360 * self.n
        y = (1 - np.arcsinh(np.tan(np.radians(lat))) / np.pi) / 2 * self.n
        return (x - self.x0) * 256, (y - self.y0) * 256

    def px_to_latlon(self, px, py):
        px, py = np.asarray(px, float), np.asarray(py, float)
        x = (px / 256 + self.x0) / self.n
        y = (py / 256 + self.y0) / self.n
        lon = x * 360 - 180
        lat = np.degrees(np.arctan(np.sinh(np.pi * (1 - 2 * y))))
        return lat, lon

    def ground_m_per_px(self, lat):
        return self.merc_res * math.cos(math.radians(lat))

    def geotransform_3857(self):
        """(origin_x, origin_y, res) of the top-left corner in EPSG:3857 metres."""
        return self.x0 * 256 * self.merc_res - ORIGIN, ORIGIN - self.y0 * 256 * self.merc_res, self.merc_res
