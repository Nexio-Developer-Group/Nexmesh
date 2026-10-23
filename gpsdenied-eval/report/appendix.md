### A. Reproduction commands
```
# data (synthetic NIDAR-like), from the repository root gpsdenied-eval/
python tools/fetch_esri_map.py --lat 18.5204 --lon 73.8567 --half_m 900 --z 19 --out data/nidar/map_esri_z19.png
python tools/sitl_mission.py --out data/nidar/flight_sitl            # ArduCopter SITL A->B->C->D at 200 m AGL
python tools/render_video.py --flight data/nidar/flight_sitl --map data/nidar/map_esri_z19.png --out data/nidar --stabilised
# harness tests
cd harness && python -m pytest test_harness.py
# SITL scenarios for one solution (3 seeds x 9 scenario rows)
python harness/run_sitl_all.py --solution s6 --estimates results/s6/nidar/S6-T1-<date>-s0/estimates.csv
# serial latency / real-time / constrained runs
bash report/serial_latency.sh
# report
python harness/collect.py --runs report/runs.json --out report/metrics_master.json
python harness/terrain.py --runs report/runs.json --out report/terrain.json --fig report/figures/terrain_breakdown.png
python harness/plots.py --runs report/runs.json --out report/figures
python harness/arch_svg.py report/arch
python harness/report_build.py
```
Per-solution replay commands are in each `SETUP.md` / `ANALYSIS.md`.

### B. Where the evidence is
| What | Path |
|---|---|
| Raw replay logs, estimates, metrics | `results/<solution>/nidar/<run-id>/` (`estimates.csv`, `run.json`, `metrics.json`, `log.txt`) |
| Oblique-camera stress runs (first, defective render) | `results/<solution>/nidar_oblique/` |
| SITL runs (summary, EKF-vs-truth trace, bridge log, final parameter file `params.json`; dataflash logs for seed 0 are local, git-ignored) | `results/<row>/sitl/<scenario>-s<seed>/` |
| Environment lockfiles and commit hashes | `solutions/<solution>/lock.txt`, `solutions/<solution>/COMMIT.txt` |
| Command logs / status log with every action and outcome | `STATUS.md` |
| Failure logs (setup failures with the exact error) | `solutions/<solution>/SETUP.md`, `results/s4/nidar/S4-T1-*/log.txt` |
| Harness code and tests | `harness/` (`metrics.py` is the single metric implementation) |
| Data generator | `tools/` |

### C. Licence list (every component recorded in the solution `SETUP.md`)
S1: MIT code; SuperPoint weights research-only. S2: no licence for the repository or the CAEVL fork; OpenVINS GPLv3 (isolated in `solutions/s2_uav_geolocalization/openvins_gpl/`). S3: no licence. S4: MIT. S5: none declared for code, weights or MAFS. S6: MIT / Apache-2.0 / BSD parts, LighterGlue weight licence unconfirmed. S7: YFS90 none; Bearing-UAV Apache-2.0 code; STHN MIT code. Esri World_Imagery tiles: benchmark use only, not redistributed (the report contains no imagery; imagery-derived files are git-ignored).
