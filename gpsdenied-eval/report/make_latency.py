"""Build report/latency.json from the serial runs (idle machine). Definitions: real-time = 5 Hz stream, 2-4 threads, frames arriving
while a step runs are dropped; constrained = pinned to ONE core (S5: two cores, 2,000 particles, as its script defines); CPU only, no Jetson."""
import json, os
import numpy as np
R = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "results")
def run(p):
    j = json.load(open(os.path.join(R, p, "run.json"))); l = np.array(j.get("latency_ms") or [np.nan]) / 1000.0
    return dict(p50=float(np.nanpercentile(l, 50)), p95=float(np.nanpercentile(l, 95)), proc=(100.0 * j["frames_processed"] / (j.get("frames_total") or (j["frames_processed"] + (j.get("dropped") or 0))) if (j.get("frames_total") or j.get("dropped")) else None),
                n=j["frames_processed"], rss=j.get("peak_rss_mb"), start=j.get("startup_s"), dropped=j.get("dropped"))
M = {
 "s1": dict(off="s1/nidar/S1-T3-ser-20261023-s0", rt="s1/nidar/S1-T3-ser-20261023-s0", con="s1/nidar/S1-T4rt-ser-20261023-s0", note="tracking config, 1,024 query keypoints, DINOv2-base; tile index cached (first build 15 min); constrained = 1 core"),
 "s2": dict(off="s2/nidar/S2-T8-realtime-20261023-s0", rt="s2/nidar/S2-T8-realtime-20261023-s0", con="s2/nidar/S2-T8-constrained-20261023-s0", note="OUR VIO substitute + CAEVL on 120 m tiles; steps are cheap, the fix quality is not; constrained = 1 core"),
 "s3": dict(off="s3/nidar/S3-T1lat-ser-20261023-s0", rt="s3/nidar/S3-T6-rt-ser-20261023-s0", con="s3/nidar/S3-T6-c1-ser-20261023-s0", note="embedding index cached (first build 700 s); constrained = 1 core"),
 "s5": dict(off="s5/nidar/S5-lat-ser-20261023-s0", rt=None, con="s5/nidar/S5-T7-20261023-s0", note="offline row = first 8 frames at 20,000 particles (first step dominates); constrained row = 2,000 particles on 2 cores (the particle count is reduced; not comparable to the others)"),
 "s6": dict(off="s6/nidar/S6-T1lat-ser-20261023-s0", rt="s6/nidar/S6-T4-rt-ser-20261023-s0", con="s6/nidar/S6-T4-c1-ser-20261023-s0", note="tracking config; constrained = 1 core"),
}
out = {"_note": "serial runs on an idle 4-core CPU, no GPU/Jetson; real-time and constrained runs feed frames on a 5 Hz clock for 152.8 s"}
for s, m in M.items():
    off = run(m["off"]); con = run(m["con"]); rt = run(m["rt"]) if m["rt"] else None
    out[s] = dict(offline_p50_s=off["p50"], offline_p95_s=off["p95"], realtime_processed_pct=(rt or con)["proc"], constrained_p50_s=con["p50"],
                  constrained_processed_pct=con["proc"], peak_rss_mb=max(x["rss"] or 0 for x in (off, con, rt or con)), startup_s=off["start"],
                  notes=m["note"] + ("" if rt else "; real-time column uses the constrained run"))
json.dump(out, open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "latency.json"), "w"), indent=1)
for s in M: print(s, {k: (round(v, 2) if isinstance(v, float) else v) for k, v in out[s].items() if k != "notes"})
