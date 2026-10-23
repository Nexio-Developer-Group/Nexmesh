"""Resource chart (constrained replay + memory) and ablation chart, from results/ and report/latency.json."""
import json, os, sys
import numpy as np
import matplotlib; matplotlib.use("Agg")
import matplotlib.pyplot as plt
sys.path.insert(0, os.path.dirname(__file__))
import plots as P
ROOT = P.ROOT; RP = os.path.join(ROOT, "report"); os.makedirs(os.path.join(RP, "figures"), exist_ok=True)
lat = json.load(open(os.path.join(RP, "latency.json")))
sols = [s for s in ("s1", "s2", "s3", "s5", "s6") if s in lat]
fig, ax = plt.subplots(1, 3, figsize=(11, 3.6))
ax[0].bar(sols, [lat[s]["peak_rss_mb"] for s in sols], color=[P.COL[s] for s in sols]); ax[0].set_ylabel("peak memory (MB RSS)"); ax[0].set_title("Peak memory")
ax[1].bar(sols, [lat[s]["constrained_p50_s"] for s in sols], color=[P.COL[s] for s in sols]); ax[1].set_yscale("log"); ax[1].set_ylabel("p50 step time (s)"); ax[1].set_title("Constrained replay step time")
ax[2].bar(sols, [lat[s]["constrained_processed_pct"] for s in sols], color=[P.COL[s] for s in sols]); ax[2].set_ylabel("% of 5 Hz frames processed"); ax[2].set_title("Throughput (constrained, 1-2 cores)")
fig.suptitle("Resources, CPU only (no Jetson); S5 constrained = 2,000 particles on 2 cores", fontsize=9)
fig.savefig(os.path.join(RP, "figures", "all_resources.png"), dpi=130, bbox_inches="tight"); plt.close(fig)

def rec(run, key="recall_50"):
    d = json.load(open(os.path.join(ROOT, "results", run, "metrics.json"))); m = d.get("all_verified") or d
    return m[key]
A = [  # (solution, label, run dir) ; each group shares a frame set, baseline first
 ("s1", "S1 tracking, gate ON (baseline, 153 fr)", "s1/nidar/S1-T2-20261023-s0"), ("s1", "S1 gate OFF", "s1/nidar/S1-T5-20261023-s0"),
 ("s2", "S2 fused, consensus ON (764 fr)", "s2/nidar/S2-T5-consensus_on-20261023-s0"), ("s2", "S2 consensus OFF", "s2/nidar/S2-T5-consensus_off-20261023-s0"),
 ("s3", "S3 as published (153 fr)", "s3/nidar/S3-T1-20261023-s0"), ("s3", "S3 no scale normalisation", "s3/nidar/S3-T3-noscale-20261023-s0"),
 ("s3", "S3 top-1 candidate only", "s3/nidar/S3-T4-top1-20261023-s0"), ("s3", "S3 top-10 candidates", "s3/nidar/S3-T4-top10-20261023-s0"),
 ("s3", "S3 + compass de-rotation (our ext.)", "s3/nidar/S3-T1b-20261023-s0"),
 ("s5", "S5 1,000 particles (40 fr)", "s5/nidar/S5-T3-n1000-20261023-s0"), ("s5", "S5 5,000", "s5/nidar/S5-T3-n5000-20261023-s0"),
 ("s5", "S5 20,000", "s5/nidar/S5-T3-n20000-20261023-s0"), ("s5", "S5 40,000", "s5/nidar/S5-T3-n40000-20261023-s0"),
 ("s6", "S6 XFeat+LighterGlue (153 fr)", "s6/nidar/S6-T3-xfeatlg-20261023-s0"), ("s6", "S6 SuperPoint+LightGlue", "s6/nidar/S6-T3-splg-20261023-s0"),
 ("s6", "S6 offline 5 Hz, no tracking (764 fr)", "s6/nidar/S6-T1-20261023-s0"), ("s6", "S6 offline 5 Hz, tracking", "s6/nidar/S6-T4off-20261023-s0"),
]
vals = [(s, l, rec(r)) for s, l, r in A]
fig, ax = plt.subplots(figsize=(8, 6.2))
y = np.arange(len(vals))[::-1]
ax.barh(y, [v[2] for v in vals], color=[P.COL[v[0]] for v in vals]); ax.set_yticks(y); ax.set_yticklabels([v[1] for v in vals], fontsize=7)
ax.set_xlabel("recall@50 m (% of the frames in that test)"); ax.set_title("Ablations: effect of each switched-off or swapped component (frame set differs per group; see labels)", fontsize=9)
for yy, v in zip(y, vals): ax.text(v[2] + 1, yy, f"{v[2]:.1f}", va="center", fontsize=7)
ax.set_xlim(0, 112); ax.grid(axis="x", alpha=.3)
fig.savefig(os.path.join(RP, "figures", "all_ablations.png"), dpi=130, bbox_inches="tight"); plt.close(fig)
print("ok")
