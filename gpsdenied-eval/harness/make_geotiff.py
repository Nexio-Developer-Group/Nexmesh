"""Write the reference mosaic as a metric GeoTIFF (EPSG:3857). Run with an env that has rasterio+opencv."""
import sys, cv2, rasterio
from rasterio.transform import from_origin
sys.path.insert(0, __import__("os").path.dirname(__file__))
from geo import MapRef

png, js, out = sys.argv[1:4]
ref = MapRef(js); ox, oy, res = ref.geotransform_3857()
img = cv2.cvtColor(cv2.imread(png), cv2.COLOR_BGR2RGB)
with rasterio.open(out, "w", driver="GTiff", height=img.shape[0], width=img.shape[1], count=3, dtype="uint8",
                   crs="EPSG:3857", transform=from_origin(ox, oy, res, res), compress="deflate", tiled=True) as d:
    d.write(img.transpose(2, 0, 1))
print("wrote", out, img.shape, "pixel", res, "m (mercator)")
