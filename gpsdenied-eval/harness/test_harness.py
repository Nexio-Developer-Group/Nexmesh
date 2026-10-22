import os, sys, tempfile, csv
import numpy as np, cv2
sys.path.insert(0, os.path.dirname(__file__))
import metrics as M, run_replay as R


def test_haversine_known():
    assert abs(M.haversine(0, 0, 0, 1) - 111195) < 100  # 1 deg lon at equator


def test_known_offset():
    t = np.arange(0, 100.0)
    truth = {"t": t, "lat": 40 + t * 1e-5, "lon": np.full_like(t, -75.0)}
    off = 30.0 / 111195.0  # 30 m north
    est = {"t": t, "lat": truth["lat"] + off, "lon": truth["lon"], "accepted": np.ones_like(t, bool)}
    s = M.summarize(est, truth, len(t))
    assert abs(s["median"] - 30) < 0.5 and abs(s["rmse"] - 30) < 0.5
    assert s["recall_10"] == 0 and s["recall_50"] == 100 and s["fix_rate"] == 100


def test_rejected_not_scored():
    t = np.arange(10.0)
    truth = {"t": t, "lat": np.full(10, 1.0), "lon": np.full(10, 1.0)}
    acc = np.array([True] * 5 + [False] * 5)
    est = {"t": t, "lat": np.where(acc, 1.0, 2.0), "lon": np.ones(10), "accepted": acc}
    s = M.summarize(est, truth, 10)
    assert s["accepted"] == 5 and s["max"] < 1 and s["recall_10"] == 50


def test_minmax_not_run():
    sc = M.minmax_score({"a": 1.0, "b": 3.0, "c": None}, higher_better=False)
    assert sc == {"a": 10.0, "b": 0.0, "c": 0.0}


def test_dummy_replay():
    d = tempfile.mkdtemp(); fr = os.path.join(d, "frames"); os.makedirs(fr)
    t = np.arange(0, 20.0, 0.2)
    for x in t: cv2.imwrite(os.path.join(fr, f"{x:.1f}.png"), np.zeros((8, 8, 3), np.uint8))
    tp = os.path.join(d, "truth.csv")
    with open(tp, "w", newline="") as f:
        w = csv.writer(f); w.writerow(["t", "lat", "lon", "alt", "yaw"])
        for x in t: w.writerow([x, 40 + x * 1e-5, -75, 200, 0])
    os.environ["HARNESS_TRUTH"] = tp
    import dummy_adapter
    truth = R.load_truth(tp)
    est, meta = R.run(dummy_adapter.Adapter(), fr, truth, "", os.path.join(d, "out"))
    e = {k: np.array([r[k] for r in est]) for k in ("t", "lat", "lon", "accepted")}
    s = M.summarize(e, truth, len(est))
    assert s["fix_rate"] == 100 and 3 < s["rmse"] < 9  # sigma 5 m per axis -> ~7 m RMSE
    assert len(meta["latency_ms"]) == len(t)
