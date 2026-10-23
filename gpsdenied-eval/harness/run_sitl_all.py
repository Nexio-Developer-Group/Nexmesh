"""Run all SITL scenarios x seeds for one solution (sequential). Usage:
run_sitl_all.py --solution s3 --estimates <estimates.csv> [--seeds 0 1 2] [--scenarios SIM-0 ...]
Outputs results/<solution>/sitl/<scenario>-s<seed>/ (summary.json, ekf_vs_truth.csv, bridge_log, params.json; dataflash logs kept only for seed 0, git-ignored)."""
import argparse, json, os, shutil, sys, traceback
sys.path.insert(0, os.path.dirname(__file__))
import sitl_runner as S

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
ap = argparse.ArgumentParser()
ap.add_argument("--solution", required=True); ap.add_argument("--estimates", required=True)
ap.add_argument("--seeds", type=int, nargs="+", default=[0, 1, 2]); ap.add_argument("--scenarios", nargs="+", default=list(S.SCEN))
ap.add_argument("--speedup", type=int, default=8); ap.add_argument("--tag", default="")
a = ap.parse_args()
for scn in a.scenarios:
    for seed in a.seeds:
        out = os.path.join(ROOT, "results", a.solution, "sitl", f"{scn}-s{seed}{a.tag}")
        if os.path.exists(os.path.join(out, "summary.json")): continue
        print("RUN", a.solution, scn, seed, flush=True)
        try:
            S.run(scn, a.estimates, os.path.join(ROOT, "data/nidar/truth.csv"), os.path.join(ROOT, "data/nidar/flight_sitl/mission.json"),
                  seed, out, speedup=a.speedup)
        except Exception as e:
            os.makedirs(out, exist_ok=True)
            json.dump({"scenario": scn, "seed": seed, "error": repr(e), "trace": traceback.format_exc()[-1500:], "result": "not run / failed"},
                      open(os.path.join(out, "summary.json"), "w"), indent=1)
        logs = os.path.join(out, "sitl_wd", "logs")
        if seed != 0 and os.path.isdir(logs): shutil.rmtree(logs, ignore_errors=True)
        for junk in ("eeprom.bin", "terrain"):
            pth = os.path.join(out, "sitl_wd", junk)
            if os.path.isfile(pth): os.remove(pth)
            elif os.path.isdir(pth): shutil.rmtree(pth, ignore_errors=True)
