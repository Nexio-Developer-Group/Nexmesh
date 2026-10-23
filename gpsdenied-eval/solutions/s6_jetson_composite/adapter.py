"""Adapter for 'our composite' (S6): MegaLoc + XFeat/LighterGlue + planar PnP + consistency gate (+ tracking window).
Config keys: see s6lib.DEFAULT. Extra: att_source = 'truth60' (IMU attitude incl. roll/pitch, simulated from truth60.csv)
or 'hints' (harness hints['att'], which carry roll=pitch=0)."""
import os, sys, math, json
import numpy as np, cv2
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import s6lib as L


class Adapter:
    def init(self, map_path, config):
        cfg = dict(config or {})
        self.att_source = cfg.pop("att_source", os.environ.get("S6_ATT", "truth60"))
        cfg.setdefault("att_reliable", self.att_source == "truth60")
        self.pipe = L.Pipeline(map_path, cfg)
        self.t60 = None
        if self.att_source == "truth60":
            self.t60 = np.genfromtxt(os.path.join(HERE, "..", "..", "data", "nidar", "truth60.csv"), delimiter=",", names=True)
        self.extras = []
        self.init_info = self.pipe.t_init

    def step(self, frame, timestamp, hints):
        g = frame if frame.ndim == 2 else cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY)
        baro = float(hints["baro_alt"]) if hints else self.pipe.cfg["default_alt"]
        yaw = float(hints["att"][2]) if hints else 0.0
        roll = pitch = 0.0
        if self.t60 is not None:
            roll = float(np.interp(timestamp, self.t60["t"], self.t60["roll"])); pitch = float(np.interp(timestamp, self.t60["t"], self.t60["pitch"]))
        elif hints:
            roll, pitch = float(hints["att"][0]), float(hints["att"][1])
        r = self.pipe.locate(g, timestamp, baro, (roll, pitch, yaw))
        T = r.pop("T", {})
        out = dict(r)
        out["conf"] = float(min(1.0, r["n_inl"] / 100.0)) if r["accepted"] else 0.0
        if not r["accepted"]:
            out.update(lat=float("nan"), lon=float("nan"), alt=None, yaw=None)
        out["T"] = T
        self.extras.append(dict(t=timestamp, **{k: v for k, v in out.items() if k != "T"}, **{"t_" + k: v for k, v in T.items()}))
        return out
