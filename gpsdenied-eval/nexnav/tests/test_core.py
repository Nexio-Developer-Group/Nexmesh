import csv, json, math, os, sys
import numpy as np
import pytest
cv2 = pytest.importorskip("cv2")
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "src"))
from nexnav import config as C, geo, fusion, recorder
from nexnav.engines.base import Fix


def test_config_defaults_validate_and_template():
    cfg = C.default_config(); cfg["map"]["path"] = "x.png"; cfg["camera"]["hfov_deg"] = 60
    assert C.validate(cfg) == []
    txt = C.template_yaml(); assert "engines.s6.tile_scales_m" in txt and "fusion.mode" in txt
    assert "| `run.engines` |" in C.config_markdown()


def test_config_rejects_typos_and_missing(tmp_path):
    p = tmp_path / "c.yaml"; p.write_text("run: {engines: both}\nmap: {path: m.png}\ncamera: {hfov_deg: 60}\nengines: {s6: {topkk: 3}}\n")
    with pytest.raises(ValueError, match="unknown config key: engines.s6.topkk"): C.load_config(str(p))
    p.write_text("run: {engines: both}\ncamera: {hfov_deg: 60}\n")
    with pytest.raises(ValueError, match="map.path is required"): C.load_config(str(p))
    p.write_text("run: {engines: both}\nmap: {path: m.png}\ncamera: {hfov_deg: 60}\nengines: {s5: {repo_path: /x}}\n")
    assert C.load_config(str(p))["run"]["engines"] == ["s6", "s5"]


def _map(tmp_path):
    img = (np.random.default_rng(0).random((512, 768, 3)) * 255).astype(np.uint8); cv2.imwrite(str(tmp_path / "m.png"), img)
    json.dump({"z": 19, "tile_x0": 369700, "tile_y0": 234600, "width": 768, "height": 512}, open(tmp_path / "m.json", "w"))
    return str(tmp_path / "m.png")


def test_geomap_roundtrip_and_scale(tmp_path):
    m = geo.GeoMap.load(_map(tmp_path))
    la, lo = m.px_to_latlon(300.0, 200.0); x, y = m.latlon_to_px(la, lo)
    assert abs(x - 300) < 1e-6 and abs(y - 200) < 1e-6
    assert 0.25 < m.ground_m_per_px() < 0.32        # zoom 19 at ~18.5 N
    r = m.resampled(0.5); assert r.width == 384 and abs(r.px_to_latlon(150, 100)[0] - m.px_to_latlon(300, 200)[0]) < 1e-9


def test_geo_helpers():
    assert abs(geo.haversine_m(0, 0, 0, 1) - 111195) < 100
    la, lo = geo.offset_latlon(18.5, 73.8, 100.0, 50.0); n, e = geo.enu_delta_m(18.5, 73.8, la, lo)
    assert abs(n - 100) < 0.5 and abs(e - 50) < 0.5


def _fix(engine, lat, lon, ok=True, conf=0.8, t=1.0): return Fix(t, engine, ok, lat, lon, conf=conf)


def test_fusion_modes():
    cfg = C.default_config(); cfg["run"]["engines"] = ["s6", "s5"]
    f = fusion.Fuser(cfg, ["s6", "s5"])
    assert f.update(_fix("s6", 18.5, 73.8)).quality == "single"
    z = f.update(_fix("s5", *geo.offset_latlon(18.5, 73.8, 10.0, 0.0), conf=0.8)); assert z.quality == "agree" and 4 < z.disagree_m < 11
    z = f.update(_fix("s5", *geo.offset_latlon(18.5, 73.8, 500.0, 0.0), conf=0.8)); assert z.quality == "conflict" and z.source == "s6"
    cfg["fusion"]["mode"] = "consensus"; g = fusion.Fuser(cfg, ["s6", "s5"]); g.update(_fix("s6", 18.5, 73.8))
    assert not g.update(_fix("s5", *geo.offset_latlon(18.5, 73.8, 500.0, 0.0))).accepted
    cfg["fusion"]["mode"] = "primary_fallback"; h = fusion.Fuser(cfg, ["s6", "s5"])
    assert h.update(_fix("s5", 18.5, 73.8)).source == "s5"        # primary has no fix: fall back
    h.update(_fix("s6", 18.5, 73.8, ok=False)); assert h.update(_fix("s5", 18.5, 73.8)).source == "s5"


def test_fusion_carries_older_fix_forward():
    cfg = C.default_config(); f = fusion.Fuser(cfg, ["s6", "s5"])
    a = _fix("s6", 18.5, 73.8, t=1.0); a.info.update(vn=10.0, ve=0.0)
    b = _fix("s5", *geo.offset_latlon(18.5, 73.8, 20.0, 0.0), t=3.0)      # 2 s later, the vehicle moved 20 m north
    f.update(a); z = f.update(b)
    assert z.quality == "agree" and z.disagree_m < 1.0 and z.t == 3.0      # without carrying the old fix forward they would be 20 m apart


def test_correction_sign(tmp_path):
    from nexnav.telemetry import GpsSample
    g = GpsSample(0, 18.5, 73.8, 3); la, lo = geo.offset_latlon(18.5, 73.8, 30.0, -40.0)   # estimate 30 m north, 40 m west of the GPS
    c = recorder.Recorder._compare(la, lo, (g, 0.1))
    assert abs(float(c["err_m"]) - 50) < 0.5 and float(c["corr_north_m"]) < -29 and float(c["corr_east_m"]) > 39    # correction moves the estimate back onto the GPS
    assert float(c["corr_lat_deg"]) < 0 and float(c["corr_lon_deg"]) > 0


def test_end_to_end_dummy_replay(tmp_path):
    """Folder camera + csv telemetry + dummy engine -> run folder with estimates, comparison and summary."""
    from nexnav.runner import Runner
    mp = _map(tmp_path); fr = tmp_path / "frames"; fr.mkdir()
    m = geo.GeoMap.load(mp); la0, lo0 = m.px_to_latlon(300, 250)
    rows = []
    for k in range(20):
        t = k * 0.2; lat, lon = geo.offset_latlon(la0, lo0, 2.0 * t, 0.0)
        rows.append((t, lat, lon, 200.0, 90.0)); cv2.imwrite(str(fr / f"{t:.3f}.png"), np.zeros((80, 128, 3), np.uint8))
    with open(tmp_path / "tel.csv", "w", newline="") as f:
        w = csv.writer(f); w.writerow(["t", "lat", "lon", "alt", "yaw"]); w.writerows(rows)
    cfg = C.load_config(None, {"run.engines": ["dummy"], "map.path": mp, "camera.source": f"folder:{fr}", "camera.hfov_deg": 60, "telemetry.source": "csv",
                               "telemetry.csv.path": str(tmp_path / "tel.csv"), "run.output_dir": str(tmp_path / "runs"), "run.realtime": False, "map.cache_dir": str(tmp_path / "cache")}) if False else None
    cfg = C.default_config(); cfg["run"].update(engines=["dummy"], output_dir=str(tmp_path / "runs"), realtime=False, name="t"); cfg["map"].update(path=mp, cache_dir=str(tmp_path / "cache"))
    cfg["camera"].update(source=f"folder:{fr}", hfov_deg=60); cfg["telemetry"]["source"] = "csv"; cfg["telemetry"]["csv"]["path"] = str(tmp_path / "tel.csv")
    s = Runner(cfg).run()
    d = s["run_dir"]; assert os.path.isfile(os.path.join(d, "estimates_dummy.csv")) and os.path.isfile(os.path.join(d, "summary.json")) and os.path.isfile(os.path.join(d, "config.resolved.yaml"))
    est = list(csv.DictReader(open(os.path.join(d, "estimates_dummy.csv")))); assert len(est) == 20
    errs = [float(r["err_m"]) for r in est]; assert all(abs(e - math.hypot(5.0, 3.0)) < 0.5 for e in errs)    # dummy offset (5 N, 3 E)
    assert abs(float(est[0]["corr_north_m"]) + 5.0) < 0.5 and abs(float(est[0]["corr_east_m"]) + 3.0) < 0.5
    assert s["engines"]["dummy"]["accepted"] == 20 and abs(s["engines"]["dummy"]["err_median_m"] - 5.83) < 0.3
