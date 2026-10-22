"""Shared metrics (runbook section 12). Positions in degrees; errors in metres."""
import numpy as np

R_EARTH = 6371008.8


def haversine(lat1, lon1, lat2, lon2):
    p1, p2 = np.radians(lat1), np.radians(lat2)
    dphi, dl = p2 - p1, np.radians(np.asarray(lon2) - lon1)
    a = np.sin(dphi / 2) ** 2 + np.cos(p1) * np.cos(p2) * np.sin(dl / 2) ** 2
    return 2 * R_EARTH * np.arcsin(np.sqrt(a))


def interp_truth(truth, t):
    return (np.interp(t, truth["t"], truth["lat"]), np.interp(t, truth["t"], truth["lon"]))


def position_errors(est, truth):
    """est/truth: dicts of arrays. Returns error (m) per estimate, interpolated truth."""
    tl, tn = interp_truth(truth, np.asarray(est["t"]))
    return haversine(tl, tn, np.asarray(est["lat"]), np.asarray(est["lon"]))


def summarize(est, truth, n_frames, radii=(10, 50, 100)):
    acc = np.asarray(est["accepted"], bool)
    sel = {k: np.asarray(v)[acc] for k, v in est.items()}
    err = position_errors(sel, truth) if acc.any() else np.array([])
    out = {"frames": int(n_frames), "accepted": int(acc.sum()),
           "fix_rate": 100.0 * acc.sum() / max(n_frames, 1)}
    if err.size:
        out.update(median=float(np.median(err)), p95=float(np.percentile(err, 95)),
                   rmse=float(np.sqrt(np.mean(err ** 2))), max=float(err.max()),
                   precision_100=float(100.0 * np.mean(err <= 100)))
    else:
        out.update(median=None, p95=None, rmse=None, max=None, precision_100=None)
    for r in radii:  # recall over ALL frames
        out[f"recall_{r}"] = 100.0 * float(np.sum(err <= r)) / max(n_frames, 1)
    ok = np.where(err <= 100)[0]
    out["time_to_first_fix"] = float(sel["t"][ok[0]] - est["t"][0]) if ok.size else None
    return out


def latency_stats(ms):
    ms = np.asarray(ms, float)
    return {"p50": float(np.percentile(ms, 50)), "p95": float(np.percentile(ms, 95))}


def drift_per_km(est_xy_err, path_len_m):
    return float(est_xy_err[-1] / (path_len_m / 1000.0)) if path_len_m > 0 else None


def path_length(lat, lon):
    return float(np.sum(haversine(lat[:-1], lon[:-1], lat[1:], lon[1:])))


def minmax_score(values, higher_better):
    """0-10 min-max across solutions; None -> 0 ('not run')."""
    v = {k: x for k, x in values.items() if x is not None}
    if not v:
        return {k: 0.0 for k in values}
    lo, hi = min(v.values()), max(v.values())
    out = {}
    for k, x in values.items():
        if x is None: out[k] = 0.0
        elif hi == lo: out[k] = 10.0
        else:
            s = (x - lo) / (hi - lo)
            out[k] = 10.0 * (s if higher_better else 1 - s)
    return out


def weighted_ranking(criteria, weights):
    names = list(next(iter(criteria.values())))
    tot = {s: sum(weights[c] * criteria[c][s] for c in weights) / sum(weights.values()) for s in names}
    return sorted(tot.items(), key=lambda kv: -kv[1])


def sensitivity(criteria, weights, bump=10.0):
    res = {}
    for c in weights:
        w = {k: (v + bump if k == c else max(v - bump / (len(weights) - 1), 0)) for k, v in weights.items()}
        res[c] = weighted_ranking(criteria, w)
    return res
