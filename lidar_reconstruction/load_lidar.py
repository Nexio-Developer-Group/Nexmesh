"""Load the JSONL scan log into flat NumPy arrays (cached as .npz). Raw file is never modified."""
import json, os
import numpy as np


def load_jsonl(path, cache=None):
    if cache and os.path.exists(cache):
        z = np.load(cache)
        return {k: z[k] for k in z.files}
    ang, rng, t0, t1, off = [], [], [], [], [0]
    skipped = 0
    with open(path) as f:
        for line in f:
            try:
                j = json.loads(line)
                a, r = j["a"], j["r"]
                if len(a) != len(r):
                    raise ValueError("length mismatch")
            except (ValueError, KeyError):
                skipped += 1
                continue
            ang.append(np.asarray(a, np.int32)); rng.append(np.asarray(r, np.int32))
            t0.append(j["t0"]); t1.append(j["t1"]); off.append(off[-1] + len(a))
    d = dict(a_raw=np.concatenate(ang), r_raw=np.concatenate(rng), t0=np.array(t0), t1=np.array(t1),
             offsets=np.array(off, np.int64), skipped=np.array(skipped))
    if cache:
        np.savez_compressed(cache, **d)
    return d


def scan_xy(d, i, cfg):
    """Sensor-frame (x,y) in metres of scan i, plus per-point time. x=r cos(th), y=r sin(th), th=sign*a+offset."""
    s = cfg["sensor"]; lo, hi = d["offsets"][i], d["offsets"][i + 1]
    r = d["r_raw"][lo:hi] * s["range_unit_m"]
    th = np.deg2rad(d["a_raw"][lo:hi] * s["angle_unit_deg"] * s["angle_sign"] + s["angle_offset_deg"])
    ok = (r >= s["min_range_m"]) & (r <= s["max_range_m"])
    t = np.linspace(d["t0"][i], d["t1"][i], hi - lo)
    return np.c_[r * np.cos(th), r * np.sin(th)][ok], t[ok]
