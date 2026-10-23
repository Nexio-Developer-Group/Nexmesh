"""Uniform metrics for every solution's primary run, computed with harness/metrics.py on a COMMON frame set
(1 Hz grid t = 0,1,2,... s, 153 frames) so every solution is scored on the same inputs; also the full-rate numbers
when a run has them. Reads runs.json. Writes report/metrics_master.json."""
import argparse, csv, glob, json, os, sys
import numpy as np
sys.path.insert(0, os.path.dirname(__file__))
import metrics as M
import plots as P

ROOT = P.ROOT


def in_windows(t, w):
    m = np.zeros(len(t), bool)
    for e in w["blur"] + w["haze"]: m |= (t >= e["t0"] - 1.5) & (t <= e["t1"] + 1.5)
    return m


def subset(est, mask):
    return {k: v[mask] for k, v in est.items()}


def summarize_set(est, truth, n_frames):
    s = M.summarize(est, truth, n_frames)
    return s


def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--runs", required=True); ap.add_argument("--out", required=True)
    a = ap.parse_args()
    runs = json.load(open(a.runs)); truth = P.load_truth(); w = P.weather()
    grid = np.arange(0, truth["t"][-1] + 1e-6, 1.0)  # common 1 Hz frame set
    out = {}
    for sol, r in runs.items():
        d = r.get("nidar")
        d = d if (d is None or os.path.isabs(d)) else os.path.join(ROOT, d)
        if not d or not os.path.exists(os.path.join(d, "estimates.csv")): out[sol] = {"label": r["label"], "status": "not run"}; continue
        est = P.load_est(d)
        # keep only frames on the common grid (nearest within 0.05 s)
        idx = [i for i, t in enumerate(est["t"]) if abs(t - round(t)) < 0.05]
        e1 = subset(est, np.array(idx, int)) if idx else None
        res = {"label": r["label"], "run": os.path.relpath(d, ROOT), "frames_in_run": int(len(est["t"]))}
        if e1 is not None and len(e1["t"]):
            ok = np.isin(np.round(e1["t"]).astype(int), grid.astype(int))
            e1 = subset(e1, ok)
            res["common_1hz"] = M.summarize(e1, truth, len(grid) if len(e1["t"]) >= 0.9 * len(grid) else len(e1["t"]))
            res["common_1hz"]["frames_available"] = int(len(e1["t"]))
            wm = in_windows(e1["t"], w)
            for nm, msk in (("weather_windows", wm), ("clear", ~wm)):
                if msk.any():
                    s = M.summarize(subset(e1, msk), truth, int(msk.sum()))
                    res[nm] = {k: s[k] for k in ("frames", "accepted", "fix_rate", "median", "p95", "precision_100", "recall_50", "recall_100")}
        res["full_rate"] = M.summarize(est, truth, len(est["t"]))
        rj = os.path.join(d, "run.json")
        if os.path.exists(rj):
            j = json.load(open(rj)); lat = j.get("latency_ms") or []
            if lat: res["latency_ms"] = M.latency_stats(lat)
            res["peak_rss_mb"] = j.get("peak_rss_mb"); res["startup_s"] = j.get("startup_s")
        out[sol] = res
    os.makedirs(os.path.dirname(a.out), exist_ok=True)
    json.dump(out, open(a.out, "w"), indent=1)
    print(f"{'sol':8} {'frames':>6} {'fix%':>6} {'med':>7} {'p95':>8} {'prec100':>8} {'rec50':>7} {'rec100':>7}")
    for sol, r in out.items():
        c = r.get("common_1hz")
        if not c: print(sol, r.get("status")); continue
        f = lambda x: "  -  " if x is None else f"{x:.1f}"
        print(f"{sol:8} {c['frames']:6d} {c['fix_rate']:6.1f} {f(c['median']):>7} {f(c['p95']):>8} {f(c['precision_100']):>8} {c['recall_50']:7.1f} {c['recall_100']:7.1f}")


if __name__ == "__main__":
    main()
