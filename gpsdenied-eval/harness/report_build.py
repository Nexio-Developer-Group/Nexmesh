"""Build the final report from results/ with a script (no hand-typed numbers).
Outputs report/REPORT.md and report/REPORT.html (self-contained; images embedded). Usage: python report_build.py"""
import base64, csv, glob, json, os, re, sys, math
import numpy as np
sys.path.insert(0, os.path.dirname(__file__))
import metrics as M

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
RP = os.path.join(ROOT, "report")
runs = json.load(open(os.path.join(RP, "runs.json")))
master = json.load(open(os.path.join(RP, "metrics_master.json")))
terrain = json.load(open(os.path.join(RP, "terrain.json")))
meta = json.load(open(os.path.join(RP, "meta.json")))
lat = json.load(open(os.path.join(RP, "latency.json")))
SOL = ["s1", "s2", "s3", "s4", "s5", "s6"]
NAME = {"s1": "S1 koksalan/gnss-denied-nav", "s2": "S2 uav-geolocalization (CAEVL + VIO)", "s3": "S3 tile-matching (Shuhaieva)",
        "s4": "S4 hmakelin/gisnav", "s5": "S5 SWA-PF", "s6": "S6 our composite (Jetson baseline)"}
SHORT = {"s1": "S1", "s2": "S2", "s3": "S3", "s4": "S4", "s5": "S5", "s6": "S6"}
FOLDER = {"s1": "s1_gnss_denied_nav", "s2": "s2_uav_geolocalization", "s3": "s3_tile_matching", "s4": "s4_gisnav", "s5": "s5_swa_pf", "s6": "s6_jetson_composite"}
SITL_ROWS = ["dummy", "s1", "s2", "s2vio", "s3", "s3b", "s5", "s6"]
SITL_LABEL = {"dummy": "Control: truth + 5 m noise fixes", "s1": "S1", "s2": "S2 (fused)", "s2vio": "S2 VIO-only track (no map fixes)", "s3": "S3 (as published)",
              "s3b": "S3 + compass de-rotation (our extension)", "s5": "S5", "s6": "S6"}
SCN = ["SIM-0", "SIM-1", "SIM-2", "SIM-3", "SIM-4a", "SIM-4b", "SIM-5", "SIM-6", "SIM-7"]


def f(x, nd=1, none="–"):
    return none if x is None or (isinstance(x, float) and math.isnan(x)) else f"{x:.{nd}f}"


def fig(name, caption, local=False):
    p = os.path.join(RP, "figures", name)
    if not os.path.exists(p): return f"*(figure {name} missing)*\n"
    b = base64.b64encode(open(p, "rb").read()).decode()
    return f'<figure><img alt="{caption}" src="data:image/png;base64,{b}"/><figcaption>{caption}</figcaption></figure>\n'


def svg(sol):
    p = os.path.join(RP, "arch", f"{sol}.svg")
    return open(p).read() if os.path.exists(p) else ""


# ---------- SITL aggregation ----------
_MIS = json.load(open(os.path.join(ROOT, "data/nidar/flight_sitl/mission.json")))["waypoints"]
_D = _MIS["D"]


def reached_d(trace, summ, radius=30.0):
    """Mission completed = the vehicle's TRUE position came within 30 m of waypoint D before any EKF failsafe/LAND (the runner's own
    4 m arrival test is stricter than the fix quality of some solutions allows)."""
    if not os.path.exists(trace): return bool(summ.get("mission_completed"))
    t_fs = summ.get("failsafe_or_land_t")
    for r in csv.DictReader(open(trace)):
        t = float(r["t_mission"])
        if t_fs is not None and t > t_fs: return False
        dn = math.radians(float(r["true_lat"]) - _D[0]) * 6378137.0
        de = math.radians(float(r["true_lon"]) - _D[1]) * 6378137.0 * math.cos(math.radians(_D[0]))
        if math.hypot(dn, de) <= radius: return True
    return False

def sitl(sol):
    out = {}
    for scn in SCN:
        rs = []
        for fpath in sorted(glob.glob(os.path.join(ROOT, "results", sol, "sitl", f"{scn}-s*/summary.json"))):
            d = json.load(open(fpath))
            if "error" in d: continue
            d["mission_completed"] = reached_d(os.path.join(os.path.dirname(fpath), "ekf_vs_truth.csv"), d)  # re-derived: within 30 m of D before any failsafe
            rs.append(d)
        if rs:
            out[scn] = dict(n=len(rs), max_err=np.mean([r["max_ekf_err_m"] for r in rs if r["max_ekf_err_m"] is not None] or [np.nan]),
                            max_err_sd=np.std([r["max_ekf_err_m"] for r in rs if r["max_ekf_err_m"] is not None] or [np.nan]),
                            completed=float(np.mean([r["mission_completed"] for r in rs])),
                            failsafe=float(np.mean([r["failsafe_or_land_t"] is not None for r in rs])),
                            dist_home=float(np.mean([r["dist_home_end_m"] for r in rs if r["dist_home_end_m"] is not None] or [np.nan])),
                            fixes=float(np.mean([r["fixes_sent"] for r in rs])))
    return out


SIT = {s: sitl(s) for s in SITL_ROWS}

# ---------- scoring ----------
def common(sol, key):
    c = master.get(sol, {}).get("common_1hz")
    return None if not c else c.get(key)


def norm(vals, higher):
    return M.minmax_score(vals, higher)


def score_all(weights):
    sc = {}
    # accuracy
    A = [norm({s: common(s, "median") for s in SOL}, False), norm({s: common(s, "p95") for s in SOL}, False), norm({s: common(s, "recall_50") for s in SOL}, True)]
    sc["Accuracy"] = {s: float(np.mean([a[s] for a in A])) for s in SOL}
    # robustness
    prec = norm({s: common(s, "precision_100") for s in SOL}, True)
    wr = norm({s: (master.get(s, {}).get("weather_windows") or {}).get("recall_100") for s in SOL}, True)
    tw = {}
    for s in SOL:
        t = terrain.get(s)
        tw[s] = min([(v or {"recall_50": 0})["recall_50"] for v in t.values()]) if t else None
    ter = norm(tw, True)
    sc["Robustness"] = {s: float(np.mean([prec[s], wr[s], ter[s]])) for s in SOL}
    # real-time / edge
    L = {s: lat.get(s) for s in SOL}
    cp = norm({s: (L[s] or {}).get("constrained_p50_s") for s in SOL}, False)
    rt = norm({s: (L[s] or {}).get("realtime_processed_pct") for s in SOL}, True)
    mem = norm({s: (L[s] or {}).get("peak_rss_mb") for s in SOL}, False)
    sc["Real-time and edge fit"] = {s: float(np.mean([cp[s], rt[s], mem[s]])) for s in SOL}
    # SITL
    comp, err = {}, {}
    for s in SOL:
        sit = SIT.get(s) or {}
        ks = [k for k in ("SIM-2", "SIM-3", "SIM-4a", "SIM-4b", "SIM-5", "SIM-6", "SIM-7") if k in sit]
        if not ks: comp[s] = None; err[s] = None; continue
        comp[s] = float(np.mean([sit[k]["completed"] for k in ks])); err[s] = float(np.mean([math.log10(max(sit[k]["max_err"], 1.0)) for k in ks]))
    c1, c2 = norm(comp, True), norm(err, False)
    sc["SITL outcome"] = {s: float(np.mean([c1[s], c2[s]])) for s in SOL}
    # integration / docs / licence
    eff = {s: (None if meta[s].get("not_run") or meta[s].get("setup_min") is None else meta[s]["setup_min"] + 10 * meta[s]["failed_steps"]) for s in SOL}
    e = norm(eff, False)
    doc = {s: sum(meta[s]["rubric"]) / 12 * 10 for s in SOL}
    lic = {s: {"permissive": 10, "copyleft": 5, "non-commercial": 3, "none": 1}[meta[s]["licence"]] for s in SOL}
    mat = {s: meta[s]["maturity"] for s in SOL}
    sc["Integration, documentation and licence"] = {s: float(np.mean([e[s], doc[s], lic[s], mat[s]])) for s in SOL}
    total = {s: sum(weights[k] * sc[k][s] for k in weights) / sum(weights.values()) for s in SOL}
    return sc, total


W0 = {"Accuracy": 30, "Robustness": 20, "Real-time and edge fit": 20, "SITL outcome": 15, "Integration, documentation and licence": 15}
SC, TOT = score_all(W0)
rank = sorted(TOT.items(), key=lambda kv: -kv[1])
# sensitivity: each weight +10, others reduced proportionally
SENS = {}
for k in W0:
    w = {kk: (v + 10 if kk == k else v - 10 / (len(W0) - 1)) for kk, v in W0.items()}
    _, t = score_all(w); SENS[k] = sorted(t.items(), key=lambda kv: -kv[1])
winners = {k: v[0][0] for k, v in SENS.items()}
stable = all(w == rank[0][0] for w in winners.values())

# ---------- text ----------
def read(p):
    return open(p).read() if os.path.exists(p) else f"*(missing: {os.path.relpath(p, ROOT)})*"


def demote(md, by=2):
    out = []
    infence = False
    for ln in md.splitlines():
        if ln.startswith("```"): infence = not infence
        if not infence and ln.startswith("#"):
            n = len(ln) - len(ln.lstrip("#")); ln = "#" * min(n + by, 6) + ln[n:]
        out.append(ln)
    return "\n".join(out)


def master_table():
    h = "| Solution | frames | fix rate % | accepted-fix median m | p95 m | recall@10 % | recall@50 % | recall@100 % | accepted precision@100 % | time to first fix s |\n|---|---|---|---|---|---|---|---|---|---|\n"
    rows = []
    for s in SOL:
        c = common(s, "frames")
        if c is None: rows.append(f"| {NAME[s]} | – | not run | – | – | – | – | – | – | – |"); continue
        m = master[s]["common_1hz"]
        rows.append(f"| {NAME[s]} | {m['frames']} | {f(m['fix_rate'])} | {f(m['median'],2)} | {f(m['p95'],1)} | {f(m['recall_10'])} | {f(m['recall_50'])} | {f(m['recall_100'])} | {f(m['precision_100'])} | {f(m['time_to_first_fix'])} |")
    return h + "\n".join(rows)


def fullrate_table():
    h = "| Solution | run | frames | fix rate % | median m | p95 m | RMSE m | max m |\n|---|---|---|---|---|---|---|---|\n"
    rows = []
    for s in SOL:
        m = master.get(s, {})
        if "full_rate" not in m: continue
        r = m["full_rate"]; rows.append(f"| {NAME[s]} | `{m['run'].split('/')[-1]}` | {r['frames']} | {f(r['fix_rate'])} | {f(r['median'],2)} | {f(r['p95'])} | {f(r['rmse'])} | {f(r['max'])} |")
    return h + "\n".join(rows)


def weather_table():
    h = "| Solution | frames in blur/haze windows | recall@100 % (windows) | fix rate % (windows) | frames clear | recall@100 % (clear) | fix rate % (clear) |\n|---|---|---|---|---|---|---|\n"
    rows = []
    for s in SOL:
        m = master.get(s, {}); w = m.get("weather_windows"); c = m.get("clear")
        if not w: continue
        rows.append(f"| {NAME[s]} | {w['frames']} | {f(w['recall_100'])} | {f(w['fix_rate'])} | {c['frames']} | {f(c['recall_100'])} | {f(c['fix_rate'])} |")
    return h + "\n".join(rows)


def terrain_table():
    n = terrain["frames_per_class_1hz"]
    h = "| Solution | " + " | ".join(f"{k} ({n[k]} fr) recall@50 %" for k in ("building", "vegetation", "water", "mixed")) + " |\n|---|---|---|---|---|\n"
    rows = []
    for s in SOL:
        t = terrain.get(s)
        if not t: continue
        rows.append(f"| {NAME[s]} | " + " | ".join(f((t[k] or {}).get('recall_50')) for k in ("building", "vegetation", "water", "mixed")) + " |")
    return h + "\n".join(rows)


def latency_table():
    h = "| Solution | clean offline p50 / p95 step (s) | real-time 5 Hz: frames processed % | constrained (1-2 cores) p50 step s | constrained frames processed % | peak RSS MB | startup / index build s | notes |\n|---|---|---|---|---|---|---|---|\n"
    rows = []
    for s in SOL:
        l = lat.get(s)
        if not l: rows.append(f"| {NAME[s]} | not run | – | – | – | – | – | – |"); continue
        rows.append(f"| {NAME[s]} | {f(l.get('offline_p50_s'),2)} / {f(l.get('offline_p95_s'),2)} | {f(l.get('realtime_processed_pct'))} | {f(l.get('constrained_p50_s'),2)} | {f(l.get('constrained_processed_pct'))} | {f(l.get('peak_rss_mb'),0)} | {f(l.get('startup_s'),0)} | {l.get('notes','')} |")
    return h + "\n".join(rows)


def sitl_table(metric):
    h = "| Row | " + " | ".join(SCN) + " |\n|---|" + "---|" * len(SCN) + "\n"
    rows = []
    for s in SITL_ROWS:
        sit = SIT.get(s)
        if not sit: continue
        cells = []
        for k in SCN:
            r = sit.get(k)
            if not r: cells.append("–"); continue
            if metric == "err": cells.append(f"{r['max_err']:.0f} ± {r['max_err_sd']:.0f}")
            elif metric == "done": cells.append(f"{100 * r['completed']:.0f}%")
            elif metric == "fs": cells.append(f"{100 * r['failsafe']:.0f}%")
            elif metric == "home": cells.append(f"{r['dist_home']:.0f}")
        rows.append(f"| {SITL_LABEL[s]} | " + " | ".join(cells) + " |")
    return h + "\n".join(rows)


def rank_tables():
    h = "| Rank | Solution | Weighted score (0-10) | " + " | ".join(f"{k} ({W0[k]}%)" for k in W0) + " |\n|---|---|---|" + "---|" * len(W0) + "\n"
    rows = []
    for i, (s, t) in enumerate(rank, 1):
        rows.append(f"| {i} | {NAME[s]} | **{t:.2f}** | " + " | ".join(f"{SC[k][s]:.1f}" for k in W0) + " |")
    sh = "| Weight raised by 10 points | winner | order |\n|---|---|---|\n"
    srows = [f"| {k} | {SHORT[v[0][0]]} | " + " > ".join(f"{SHORT[a]} {b:.2f}" for a, b in v) + " |" for k, v in SENS.items()]
    return h + "\n".join(rows), sh + "\n".join(srows)


def docs_table():
    h = "| Solution | README / docs quality (0-12) | install / config / reproducible / algorithm | setup minutes (failed steps) | licence class | licence detail | maturity (0-10) |\n|---|---|---|---|---|---|---|\n"
    rows = []
    for s in SOL:
        m = meta[s]; r = m["rubric"]
        rows.append(f"| {NAME[s]} | {sum(r)} | {r[0]} / {r[1]} / {r[2]} / {r[3]} | {m['setup_min'] if m['setup_min'] else 'n/a (never ran)'} ({m['failed_steps']}) | {m['licence']} | {m['licence_note']} | {m['maturity']} ({m['maturity_note']}) |")
    return h + "\n".join(rows)


def best_for():
    def best(vals, hi):
        v = {s: x for s, x in vals.items() if x is not None}
        return (max if hi else min)(v, key=v.get) if v else None
    acc = best({s: common(s, "median") for s in SOL if (common(s, "fix_rate") or 0) > 20}, False)
    rec = best({s: common(s, "recall_50") for s in SOL}, True)
    lt = best({s: (lat.get(s) or {}).get("constrained_p50_s") for s in SOL}, False)
    sit = best({s: SC["SITL outcome"][s] for s in SOL if s != "s4"}, True)
    rob = best({s: SC["Robustness"][s] for s in SOL if s != "s4"}, True)
    easy = best({s: (meta[s]["setup_min"] + 10 * meta[s]["failed_steps"]) if (meta[s].get("setup_min") and not meta[s].get("not_run")) else None for s in SOL}, False)
    jet = best({s: SC["Real-time and edge fit"][s] for s in SOL if s != "s4"}, True)
    r = lambda s: SHORT[s] if s else "none"
    return ("| Purpose | Best | Basis |\n|---|---|---|\n"
            f"| Highest accuracy (lowest accepted-fix median, fix rate over 20 %) | {r(acc)} | common 1 Hz set, synthetic NIDAR-like |\n"
            f"| Highest coverage at 50 m (recall@50) | {r(rec)} | recall over all frames |\n"
            f"| Lowest latency under the constrained replay | {r(lt)} | 1-2 CPU cores, CPU only (no Jetson) |\n"
            f"| Best autopilot integration (SITL outcome score) | {r(sit)} | SIM-2..SIM-7, 3 seeds |\n"
            f"| Best for Jetson-class deployment (edge-fit score) | {r(jet)} | latency, throughput, memory; NOT measured on a Jetson |\n"
            f"| Most robust over feature-poor terrain and weather | {r(rob)} | precision, blur/haze windows, worst terrain class |\n"
            f"| Easiest to adopt (setup effort) | {r(easy)} | minutes + 10 per failed step |\n")


RT, ST = rank_tables()
BF = best_for()

# ---------- assemble ----------
def chapter(s):
    fol = os.path.join(ROOT, "solutions", FOLDER[s])
    parts = [f"## {NAME[s]}\n"]
    if s == "s5": parts.append("> **Substitute inputs:** the semantic map and the frame labels both come from the repository's own SegFormer-B0 run on imagery rendered from the same map; the paper's U-Net map labelling is not available. Odometry is noisy telemetry, not the demo's ground-truth poses. Results are optimistic.\n")
    if s == "s2": parts.append("> **Substitute inputs:** the VIO layer is **our substitute** (phase correlation + compass yaw + barometer), not OpenVINS (it built but could not be run within the disk limit). The CAEVL encoder is ours, trained about 50 minutes on a CPU for 2 epochs on 1,800 tiles (the authors: up to 20 epochs on ~250k tiles on a GPU).\n")
    if s == "s1": parts.append("> **Degraded:** the author's fine-tuned DINOv2 weights are not published; pretrained `facebook/dinov2-base` was used. Query keypoints capped at 1,024 and patch 140 m (author: 2,048 and 250 m).\n")
    if s == "s6": parts.append("> **Our composite, not an existing project.** No Jetson or GPU numbers exist; every Jetson figure is unmeasured.\n")
    parts.append("### Architecture (real data flow, vector diagram)\n\n" + svg(s) + "\n")
    for nm, cap in ((f"{s}_trajectory.png", f"{NAME[s]}: trajectory (dataset: synthetic NIDAR-like nadir; run {os.path.basename(runs[s].get('nidar','')) if s in runs else ''}; 1 Hz common frames or full run as named)"),
                    (f"{s}_error_time.png", f"{NAME[s]}: position error over time, blur (blue) and haze (grey) windows shaded"),
                    (f"{s}_error_map.png", f"{NAME[s]}: path coloured by error"),
                    (f"{s}_sitl_timeline.png", f"{NAME[s]}: SITL SIM-2 timeline, seed 0 (GPS cut at 60 s)")):
        if os.path.exists(os.path.join(RP, "figures", nm)): parts.append(fig(nm, cap))
    parts.append("\n### Analysis written by the solution's test agent (from the code actually run)\n\n" + demote(read(os.path.join(fol, "ANALYSIS.md")), 2) + "\n")
    parts.append("\n### Setup account\n\n" + demote(read(os.path.join(fol, "SETUP.md")), 2) + "\n")
    return "\n".join(parts)


md = []
md.append("# GPS-denied navigation: solution test report\n")
md.append(f"*Test run on {os.popen('date -u +%Y-%m-%d').read().strip()}. Branch `claude/new-session-peh13r`, folder `gpsdenied-eval/`. Every number comes from a logged run under `results/`; this document is generated by `harness/report_build.py`.*\n")
md.append("## 1. Executive summary\n")
md.append("**What was tested.** Six GPS-denied navigation solutions (S1-S6) and three paper-only items (S7) on the same synthetic NIDAR-like video, the same reference map and the same metric code, plus an ArduPilot SITL loop. The real NIDAR video, GPU, Jetson and the fallback datasets (MAFS, MUN-FRL, ALTO) were **not available**, so every result is on synthetic data, on CPU, with a map that is the same imagery the video was rendered from. Accuracy numbers are therefore **optimistic** and the ranking is about relative behaviour, not absolute field accuracy.\n")
top = rank[0]
md.append(f"**Ranking (owner weights).** 1. {NAME[rank[0][0]]} ({rank[0][1]:.2f}), 2. {NAME[rank[1][0]]} ({rank[1][1]:.2f}), 3. {NAME[rank[2][0]]} ({rank[2][1]:.2f}), then " + ", ".join(f"{NAME[s]} ({t:.2f})" for s, t in rank[3:]) + ". " +
          ("The sensitivity check keeps the same winner under every weight change, so a single overall winner is stated." if stable else "The sensitivity check changes the winner under at least one weighting, so no single overall winner is stated; see the sensitivity table.") + "\n")
md.append(rank_tables()[0] + "\n")
md.append("**Best for each purpose.**\n\n" + BF + "\n")
md.append("**Recommendation for the NIDAR system.** " + open(os.path.join(RP, "recommendation.md")).read().strip() + "\n" if os.path.exists(os.path.join(RP, "recommendation.md")) else "")
md.append("**What could not be tested.** " + open(os.path.join(RP, "not_tested.md")).read().strip() + "\n" if os.path.exists(os.path.join(RP, "not_tested.md")) else "")

md.append("## 2. Test setup\n")
md.append(open(os.path.join(RP, "test_setup.md")).read() if os.path.exists(os.path.join(RP, "test_setup.md")) else "")
md.append(open(os.path.join(RP, "test_matrix.md")).read() if os.path.exists(os.path.join(RP, "test_matrix.md")) else "")

for s in SOL:
    md.append(chapter(s))

md.append("## S7. Paper-only and special-purpose items (not ranked)\n")
md.append(demote(read(os.path.join(ROOT, "solutions", "s7_paper_only", "S7_REVIEW.md")), 1) + "\n")

md.append("## 3. Cross-solution comparison\n")
md.append("### 3.1 Master table: every solution on the same 153 frames (1 Hz, t = 0..152 s), metric code `harness/metrics.py`\n\nRecall is over all 153 frames (a frame without a fix counts as a miss). Median and p95 are over accepted fixes only, so read them together with the fix rate and recall.\n\n" + master_table() + "\n")
md.append("### 3.2 Full-rate runs (frames at the rate each solution actually ran; see run id)\n\n" + fullrate_table() + "\n")
md.append("### 3.3 Weather: blur and haze windows against clear flight\n\n" + weather_table() + "\n")
md.append("### 3.4 Terrain breakdown (class under the true position from the substitute semantic map, not ground-truth land cover)\n\n" + terrain_table() + "\n\n" + fig("terrain_breakdown.png", "Terrain breakdown: recall@50 m per class (1 Hz common frames; substitute semantic map)"))
md.append("### 3.5 Speed and resources (CPU only, serial runs on an otherwise idle machine; NO Jetson)\n\n" + latency_table() + "\n\n" + fig("all_latency_hist.png", "Per-step latency histograms (offline replay; shared-CPU load unless the table says otherwise)"))
md.append("### 3.6 Accuracy figures\n\n" + fig("all_error_cdf.png", "Error distribution of all solutions (dataset: synthetic NIDAR-like; primary runs; normalised by all frames of each run)") + fig("all_recall_curve.png", "Recall against error threshold, all solutions"))
md.append("### 3.7 Documentation, licence and setup effort side by side\n\n" + docs_table() + "\n")
md.append("### 3.8 Weighted ranking and sensitivity\n\nScores are min-max scaled to 0-10 across the solutions that ran each test; a solution that could not run a test gets 0 for it and is shown as *not run*. Weights are the owner's (30/20/20/15/15).\n\n"
          "- **Accuracy** = mean of median error (accepted fixes), p95 error and recall@50 m, on the common 153 frames.\n"
          "- **Robustness** = mean of accepted-fix precision@100 m, recall@100 m inside blur/haze windows, and the worst terrain-class recall@50 m. (The failure-injection tests differ between solutions, so the shared weather windows are the common basis.)\n"
          "- **Real-time and edge fit** = mean of constrained-replay p50 latency, real-time processed-frame share and peak memory. CPU only; no Jetson number exists.\n"
          "- **SITL outcome** = mean of mission-completion share and (log10) maximum EKF error over SIM-2 to SIM-7, 3 seeds each.\n"
          "- **Integration, documentation and licence** = mean of setup effort (minutes + 10 per failed step), documentation rubric, licence class (permissive 10, copyleft 5, non-commercial 3, none declared 1) and maturity (a judgement from the repository facts, 0-10).\n\n"
          + RT + "\n\n" + ST + "\n\n" + "Per-criterion scores:\n\n" + "| Solution | " + " | ".join(W0) + " |\n|---|" + "---|" * len(W0) + "\n" + "\n".join(f"| {NAME[s]} | " + " | ".join(f"{SC[k][s]:.2f}" for k in W0) + " |" for s in SOL) + "\n\n" + fig("ranking_chart.png", "Weighted score by criterion for every solution; the sensitivity table lists the order under each weight change"))
md.append("## 4. SITL results (ArduCopter 4.5.7, scenarios SIM-0 to SIM-7, 3 seeds)\n")
md.append(open(os.path.join(RP, "sitl_method.md")).read() if os.path.exists(os.path.join(RP, "sitl_method.md")) else "")
md.append("**Maximum EKF position error after GPS loss (m, mean ± sd over seeds)**\n\n" + sitl_table("err") + "\n\n**Mission completed (share of seeds whose true position came within 30 m of waypoint D before any EKF failsafe/LAND)**\n\n" + sitl_table("done") + "\n\n**EKF failsafe / LAND triggered (share of seeds)**\n\n" + sitl_table("fs") + "\n\n**Distance from home at the end of the run (m, SIM-7 = after return-to-launch)**\n\n" + sitl_table("home") + "\n\n" + fig("all_sitl_comparison.png", "SITL: maximum EKF error per solution for SIM-1 (no vision), SIM-2 and SIM-7, 3 seeds"))
md.append(open(os.path.join(RP, "findings.md")).read() if os.path.exists(os.path.join(RP, "findings.md")) else "")
md.append("## Appendices\n")
md.append(open(os.path.join(RP, "appendix.md")).read() if os.path.exists(os.path.join(RP, "appendix.md")) else "")

text = "\n".join(md)
open(os.path.join(RP, "REPORT.md"), "w").write(text)
json.dump({"rank": rank, "scores": SC, "sens": SENS, "stable": stable}, open(os.path.join(RP, "ranking.json"), "w"), indent=1)

# ranking chart
import matplotlib; matplotlib.use("Agg"); import matplotlib.pyplot as plt
fig_, ax = plt.subplots(figsize=(9, 4)); bot = np.zeros(len(SOL)); cols = ["#1f77b4", "#ff7f0e", "#2ca02c", "#d62728", "#9467bd"]
for (k, w), c in zip(W0.items(), cols):
    v = np.array([SC[k][s] * w / 100 for s in SOL]); ax.bar([SHORT[s] for s in SOL], v, bottom=bot, label=f"{k} ({w}%)", color=c); bot += v
ax.set_ylabel("weighted score (0-10)"); ax.legend(fontsize=7); ax.set_title("Weighted ranking breakdown (owner weights)")
os.makedirs(os.path.join(RP, "figures"), exist_ok=True); fig_.savefig(os.path.join(RP, "figures", "ranking_chart.png"), dpi=130, bbox_inches="tight")

# one-page decision memo
memo = ["# Decision memo: GPS-denied navigation solutions (one page)\n",
        "*Synthetic NIDAR-like data, CPU only, same-map imagery: relative evidence only. Full report: `report/REPORT.md` / `REPORT.pdf`.*\n",
        "**Ranking (owner weights 30/20/20/15/15):** " + "; ".join(f"{i}. {NAME[s_]} {t_:.2f}" for i, (s_, t_) in enumerate(rank, 1)) + (". The winner is unchanged under every +10-point weight change." if stable else ". The winner changes under at least one weighting (see report 3.8).") + "\n",
        "**Best for each purpose**\n\n" + BF + "\n",
        "**Recommendation.** " + open(os.path.join(RP, "recommendation.md")).read().strip() + "\n",
        "**Next three tests.** (1) S6 on real NIDAR video with a different-date map, thresholds frozen, held-out flight. (2) S6 on a Jetson Orin with TensorRT FP16 end to end at 5 Hz (solve the LighterGlue ONNX export first). (3) Closed-loop SITL with a Gazebo-rendered camera and a velocity/odometry aid in the EKF, S6 against S5 on feature-poor terrain with fix outages and outliers.\n",
        "**Could not be tested:** " + open(os.path.join(RP, "not_tested.md")).read().strip() + "\n"]
open(os.path.join(RP, "DECISION_MEMO.md"), "w").write("\n".join(memo))
# HTML
import markdown
html_body = markdown.markdown(text, extensions=["tables", "fenced_code", "sane_lists"])
css = open(os.path.join(RP, "report.css")).read() if os.path.exists(os.path.join(RP, "report.css")) else ""
html = f"<!doctype html><html lang='en'><head><meta charset='utf-8'><meta name='viewport' content='width=device-width,initial-scale=1'><title>GPS-denied navigation test report</title><style>{css}</style></head><body><main>{html_body}</main></body></html>"
open(os.path.join(RP, "REPORT.html"), "w").write(html)
# hosted-artifact fragment (the Artifact tool wraps it in the document skeleton): no html/head/body tags of its own
css2 = css.replace("main{max-width:1050px;margin:0 auto;padding:24px 16px 80px}", "main{max-width:1050px;margin:0 auto;padding-block:24px 80px;padding-left:max(16px,env(safe-area-inset-left,0px));padding-right:max(16px,env(safe-area-inset-right,0px))}")
art = ("<title>GPS-Denied Test Report</title><meta name='claude:viewport-fit-sides' content='cover'><style>" + css2 +
       "</style><main>" + html_body + "</main>")
open(os.path.join(RP, "REPORT_artifact.html"), "w").write(art)
# local-only variant with the failure galleries (these crops contain Esri-derived imagery: benchmark use only, never committed)
import cv2
gal = ["<h2>Failure galleries (local file only: crops contain Esri-derived imagery)</h2><p>Each montage: frame (north-up) | map crop at the true position (what it should have matched) | map crop at the pipeline's estimate. Solutions that wrote their own failure figures list them here too.</p>"]
for s_ in SOL:
    for p_ in sorted(glob.glob(os.path.join(ROOT, "results", s_, "figures", "failures", "*.png")))[:12]:
        im = cv2.imread(p_)
        if im is None: continue
        sc_ = 1100.0 / im.shape[1]; im = cv2.resize(im, None, fx=min(sc_, 1.0), fy=min(sc_, 1.0)); ok_, buf = cv2.imencode(".jpg", im, [cv2.IMWRITE_JPEG_QUALITY, 70])
        gal.append(f"<figure><img src='data:image/jpeg;base64,{base64.b64encode(buf).decode()}'/><figcaption>{SHORT[s_]}: {os.path.basename(p_)}</figcaption></figure>")
html2 = html.replace("</main>", "".join(gal) + "</main>")
open(os.path.join(RP, "REPORT_with_imagery.html"), "w").write(html2)
print("ranking:", [(SHORT[s], round(t, 2)) for s, t in rank], "stable winner:", stable)
