"""Dummy adapter: ground truth + Gaussian noise (sigma metres). Needs HARNESS_TRUTH env var."""
import os, numpy as np
from run_replay import load_truth


class Adapter:
    sigma = 5.0
    def init(self, map_path, config):
        self.truth = load_truth(os.environ["HARNESS_TRUTH"]); self.rng = np.random.default_rng(0)
    def step(self, frame, timestamp, hints):
        la = np.interp(timestamp, self.truth["t"], self.truth["lat"])
        lo = np.interp(timestamp, self.truth["t"], self.truth["lon"])
        n, e = self.rng.normal(0, self.sigma, 2)
        return dict(lat=la + n / 111320.0, lon=lo + e / (111320.0 * np.cos(np.radians(la))),
                    alt=None, yaw=None, conf=1.0, accepted=True)
