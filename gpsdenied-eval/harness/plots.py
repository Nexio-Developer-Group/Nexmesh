"""All cross-solution figures for the report (runbook section 13). Reads results/ only.
Usage: python plots.py --runs runs.json --out ../report/figures
runs.json: {"s1": {"label": "S1 gnss-denied-nav", "nidar": "results/s1/nidar/<run>", "sitl": "results/s1/sitl"}, ...}
Every figure caption (written by build_report.py) names the dataset, run id and frame count."""
import argparse, csv, glob, json, os, sys
import numpy as np
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

sys.path.insert(0, os.path.dirname(__file__))
import metrics as M
from geo import MapRef

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
COL = {"s1": "#1f77b4", "s2": "#ff7f0e", "s3": "#2ca02c", "s4": "#d62728", "s5": "#9467bd", "s6": "#8c564b", "dummy": "#7f7f7f"}


def load_truth():
    r = list(csv.DictReader(open(os.path.join(ROOT, "data/nidar/truth.csv"))))
    return {k: np.array([float(x[k]) for x in r]) for k in ("t", "lat", "lon", "alt", "yaw")}


def load_est(run_dir):
    r = list(csv.DictReader(open(os.path.join(run_dir, "estimates.csv"))))
    est = {"t": np.array([float(x["t"]) for x in r]),
           "lat": np.array([float(x["lat"]) if x["lat"] not in ("", "None") else np.nan for x in r]),
           "lon": np.array([float(x["lon"]) if x["lon"] not in ("", "None") else np.nan for x in r]),
           "accepted": np.array([x["accepted"] in ("True", "true", "1") for x in r])}
    est["accepted"] &= ~np.isnan(est["lat"])
    return est


def errors(est, truth):
    e = np.full(len(est["t"]), np.nan)
    ok = est["accepted"]
    if ok.any():
        e[ok] = M.position_errors({k: v[ok] for k, v in est.items()}, truth)
    return e


def weather(path=os.path.join(ROOT, "data/nidar/weather.json")):
    return json.load(open(path))


def shade_weather(ax, w):
    for e in w["blur"]: ax.axvspan(e["t0"], e["t1"], color="tab:blue", alpha=0.08, lw=0)
    for e in w["haze"]: ax.axvspan(e["t0"], e["t1"], color="tab:gray", alpha=0.15, lw=0)


def fig_trajectory(sol, label, run_dir, truth, ref, out, n, imagery=False):
    """Default: no imagery (Esri tiles are benchmark-only and are not redistributed in the report); local ENU metres."""
    est = load_est(run_dir)
    tx, ty = ref.latlon_to_px(truth["lat"], truth["lon"]); ok = est["accepted"]
    ex, ey = ref.latlon_to_px(est["lat"], est["lon"])
    fig, ax = plt.subplots(figsize=(7, 7))
    if imagery:
        img = plt.imread(os.path.join(ROOT, "data/nidar/map_esri_z19.png"))[..., ::-1]; ax.imshow(img, alpha=0.6); sc = 1.0; ox = oy = 0
    else:
        m = ref.ground_m_per_px(18.52); sc = m; ox, oy = tx[0], ty[0]
    X = lambda v: (v - ox) * sc if not imagery else v
    Y = lambda v: (oy - v) * sc if not imagery else v
    ax.plot(X(tx), Y(ty), "k-", lw=1.5, label="true path")
    ax.scatter(X(ex[ok]), Y(ey[ok]), s=8, c=COL.get(sol, "r"), label=f"accepted fix ({ok.sum()})", zorder=3)
    rej = (~ok) & ~np.isnan(est["lat"])
    if rej.any(): ax.scatter(X(ex[rej]), Y(ey[rej]), s=8, c="r", marker="x", label=f"not accepted ({rej.sum()})", zorder=2)
    if not imagery:
        pad = 150; ax.set_xlim(X(tx).min() - pad, X(tx).max() + pad); ax.set_ylim(Y(ty).min() - pad, Y(ty).max() + pad)
        ax.set_aspect("equal"); ax.grid(alpha=.3); ax.set_xlabel("east of start (m)"); ax.set_ylabel("north of start (m)")
    else:
        pad = 250; ax.set_xlim(tx.min() - pad, tx.max() + pad); ax.set_ylim(ty.max() + pad, ty.min() - pad); ax.axis("off")
    ax.set_title(f"{label}: trajectory (synthetic NIDAR-like)"); ax.legend(loc="best", fontsize=7)
    fig.savefig(os.path.join(out, f"{sol}_trajectory{'_map' if imagery else ''}.png"), dpi=130, bbox_inches="tight"); plt.close(fig)


def fig_error_time(sol, label, run_dir, truth, out, w):
    est = load_est(run_dir); err = errors(est, truth)
    fig, ax = plt.subplots(figsize=(8, 3))
    ax.semilogy(est["t"], np.where(np.isnan(err), np.nan, np.maximum(err, 0.5)), ".", ms=3, color=COL.get(sol, "k"))
    shade_weather(ax, w); ax.set_xlabel("time since cruise start (s)"); ax.set_ylabel("position error (m)")
    ax.set_title(f"{label}: error over time (shaded: blur=blue, haze=grey)"); ax.grid(alpha=.3)
    fig.savefig(os.path.join(out, f"{sol}_error_time.png"), dpi=130, bbox_inches="tight"); plt.close(fig)


def fig_error_map(sol, label, run_dir, truth, ref, out):
    est = load_est(run_dir); err = errors(est, truth)
    tx, ty = ref.latlon_to_px(truth["lat"], truth["lon"])
    e_at = np.interp(truth["t"], est["t"][~np.isnan(err)], err[~np.isnan(err)]) if (~np.isnan(err)).sum() > 1 else np.full(len(tx), np.nan)
    fig, ax = plt.subplots(figsize=(6, 6))
    sc = ax.scatter(tx, ty, c=np.log10(np.maximum(e_at, 1)), s=14, cmap="viridis_r", vmin=0, vmax=3)
    ax.set_aspect("equal"); ax.invert_yaxis(); ax.axis("off"); ax.set_title(f"{label}: path coloured by log10(error m)")
    plt.colorbar(sc, ax=ax, shrink=.7, label="log10 error (m)")
    fig.savefig(os.path.join(out, f"{sol}_error_map.png"), dpi=130, bbox_inches="tight"); plt.close(fig)


def fig_cdf_all(runs, truth, out):
    fig, ax = plt.subplots(figsize=(7, 4))
    for sol, r in runs.items():
        if "nidar" not in r: continue
        est = load_est(r["nidar"]); err = errors(est, truth); err = err[~np.isnan(err)]
        if not err.size: continue
        x = np.sort(err); ax.plot(x, np.arange(1, len(x) + 1) / len(est["t"]), color=COL.get(sol), label=r["label"])
    for v in (10, 50, 100): ax.axvline(v, color="k", ls=":", lw=.8)
    ax.set_xscale("log"); ax.set_xlabel("position error (m)"); ax.set_ylabel("fraction of all frames with fix within error")
    ax.set_title("Error distribution (CDF, normalised by ALL frames)"); ax.legend(fontsize=7); ax.grid(alpha=.3)
    fig.savefig(os.path.join(out, "all_error_cdf.png"), dpi=130, bbox_inches="tight"); plt.close(fig)
    # recall curve = same data, linear x
    fig, ax = plt.subplots(figsize=(7, 4))
    for sol, r in runs.items():
        if "nidar" not in r: continue
        est = load_est(r["nidar"]); err = errors(est, truth)
        xs = np.linspace(0, 200, 201)
        ax.plot(xs, [np.nansum(err <= x) / len(err) * 100 for x in xs], color=COL.get(sol), label=r["label"])
    ax.set_xlabel("error threshold R (m)"); ax.set_ylabel("recall (% of all frames)"); ax.legend(fontsize=7); ax.grid(alpha=.3)
    ax.set_title("Recall vs threshold"); fig.savefig(os.path.join(out, "all_recall_curve.png"), dpi=130, bbox_inches="tight"); plt.close(fig)


def fig_latency(runs, out):
    fig, ax = plt.subplots(figsize=(7, 4)); any_ = False
    for sol, r in runs.items():
        p = os.path.join(r.get("nidar", ""), "run.json")
        if os.path.exists(p):
            lat = np.array(json.load(open(p)).get("latency_ms", [])) / 1000.0
            if lat.size:
                ax.hist(lat, bins=40, alpha=.5, color=COL.get(sol), label=f"{r['label']} p50={np.percentile(lat,50):.2f}s p95={np.percentile(lat,95):.2f}s"); any_ = True
    if any_:
        ax.set_xlabel("step latency (s) -- CPU, offline replay, SHARED CPU LOAD unless stated"); ax.legend(fontsize=6); ax.set_title("Per-step latency histogram")
        fig.savefig(os.path.join(out, "all_latency_hist.png"), dpi=130, bbox_inches="tight")
    plt.close(fig)


def fig_sitl(runs, out):
    """SITL comparison: max EKF error per solution for SIM-2 and SIM-7 vs the dead-reckoning control (SIM-1)."""
    fig, ax = plt.subplots(figsize=(8, 4)); names = []; vals = {}
    for sol, r in runs.items():
        d = r.get("sitl")
        if not d: continue
        for scn in ("SIM-1", "SIM-2", "SIM-7"):
            xs = [json.load(open(f)).get("max_ekf_err_m") for f in glob.glob(os.path.join(d, f"{scn}-s*/summary.json"))]
            xs = [x for x in xs if x is not None]
            vals.setdefault(scn, []).append((r["label"], np.mean(xs) if xs else np.nan, np.std(xs) if xs else 0))
    if not vals: plt.close(fig); return
    labels = [v[0] for v in next(iter(vals.values()))]; x = np.arange(len(labels)); wd = 0.25
    for i, scn in enumerate(vals):
        ax.bar(x + i * wd, [v[1] for v in vals[scn]], wd, yerr=[v[2] for v in vals[scn]], label=scn)
    ax.set_xticks(x + wd); ax.set_xticklabels(labels, rotation=20, fontsize=7); ax.set_yscale("log")
    ax.set_ylabel("max EKF position error (m), mean over seeds"); ax.legend(); ax.set_title("SITL: maximum EKF error (SIM-1 = no-vision dead reckoning control)")
    fig.savefig(os.path.join(out, "all_sitl_comparison.png"), dpi=130, bbox_inches="tight"); plt.close(fig)


def fig_sitl_timeline(sol, label, d, out):
    for scn in ("SIM-2",):
        f = os.path.join(d, f"{scn}-s0", "ekf_vs_truth.csv")
        if not os.path.exists(f): continue
        rows = list(csv.DictReader(open(f)))
        t = np.array([float(r["t_mission"]) for r in rows]); e = np.array([float(r["err_m"]) for r in rows])
        fig, ax = plt.subplots(figsize=(8, 3)); ax.plot(t, e, color=COL.get(sol)); ax.axvline(60, color="r", ls="--", label="GPS cut")
        s = json.load(open(os.path.join(d, f"{scn}-s0", "summary.json")))
        if s.get("failsafe_or_land_t"): ax.axvline(s["failsafe_or_land_t"], color="k", ls=":", label="failsafe/LAND")
        ax.set_yscale("log"); ax.set_xlabel("mission time (s)"); ax.set_ylabel("EKF position error (m)"); ax.legend(); ax.set_title(f"{label}: SITL {scn} timeline (seed 0)")
        fig.savefig(os.path.join(out, f"{sol}_sitl_timeline.png"), dpi=130, bbox_inches="tight"); plt.close(fig)


def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--runs", required=True); ap.add_argument("--out", required=True)
    a = ap.parse_args(); os.makedirs(a.out, exist_ok=True)
    runs = json.load(open(a.runs)); truth = load_truth(); w = weather()
    ref = MapRef(os.path.join(ROOT, "data/nidar/map_esri_z19.json"))
    abs_ = lambda p: p if os.path.isabs(p) else os.path.join(ROOT, p)
    for r in runs.values():
        for k in ("nidar", "sitl"):
            if k in r: r[k] = abs_(r[k])
    for sol, r in runs.items():
        if "nidar" in r and os.path.exists(os.path.join(r["nidar"], "estimates.csv")):
            n = len(load_est(r["nidar"])["t"])
            fig_trajectory(sol, r["label"], r["nidar"], truth, ref, a.out, n)
            fig_error_time(sol, r["label"], r["nidar"], truth, a.out, w)
            fig_error_map(sol, r["label"], r["nidar"], truth, ref, a.out)
        if r.get("sitl"): fig_sitl_timeline(sol, r["label"], r["sitl"], a.out)
    fig_cdf_all({k: v for k, v in runs.items() if "nidar" in v and os.path.exists(os.path.join(v["nidar"], "estimates.csv"))}, truth, a.out)
    fig_latency(runs, a.out); fig_sitl(runs, a.out)


if __name__ == "__main__":
    main()
