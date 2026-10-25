# Reference maps

The engines compare the camera image with an orthophoto of the flight area. The map decides the accuracy: **use imagery of the same season, light and resolution as the camera if you can.** (The evaluation used the very imagery the video was rendered from, which is why its errors were small; expect worse in the field.)

## Formats nexnav reads
1. **GeoTIFF** (any CRS; needs `rasterio` and `pyproj`): `map.path: area.tif`.
2. **PNG/JPG + JSON sidecar** (`area.png` + `area.json`, or `map.georef`):
   * web-mercator tile mosaic: `{"z": 19, "tile_x0": 369700, "tile_y0": 234600}` (256 px tiles; what `nexnav map-fetch` writes)
   * equirectangular box: `{"lat_top":..., "lon_left":..., "lat_bottom":..., "lon_right":...}`

`nexnav map-info area.tif` prints size, metres per pixel and corner coordinates. Resolution: 0.3-0.6 m/px is plenty; the program resamples to `map.work_gsd_m` (0.283 m/px by default) on load, and caches the descriptor index per map and setting in `map.cache_dir`, so the first start on a new map takes minutes (about 0.2 s per tile on a laptop CPU, 1.7 s on a loaded CPU) and later starts take seconds.

Map size: the index grows with area (a 1.9 x 1.9 km map at 145 m tiles is 625 tiles, 21 MB). Use `map.roi: [lat_min, lon_min, lat_max, lon_max]` to cut it down.

## Getting a map
Download orthophotos you are licensed to use (national mapping agency, your own drone survey, commercial imagery). `nexnav map-fetch --url-template "https://<your tile server>/{z}/{x}/{y}.jpg" --lat .. --lon .. --half-m 1000 --zoom 19 --out area.png` builds a mosaic from an XYZ server **you** are licensed to use. Many imagery services allow benchmarking only and forbid redistribution.

## Terrain
The height used to scale the image is the altitude above the ground **under the drone**. `telemetry.altitude.offset_m` corrects for terrain that is higher or lower than the home point. Heights wrong by more than the gate allows (10 % or 12 m) make S6 reject fixes.
