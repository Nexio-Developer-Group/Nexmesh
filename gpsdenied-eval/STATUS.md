# STATUS

| Solution | P0 | P1 | P2 | P3 | P4 | P5 | P6 | P7 | P8 |
|---|---|---|---|---|---|---|---|---|---|
| harness | - | - | in progress | | | | | | |
| S1..S7 | | | | | | | | | |

## Log
- 2026-10-22 P0 intake: folders created. NIDAR video NOT found (searched data/nidar, session uploads, filesystem for "*nidar*"). Branch 3 of section 3: NIDAR lines will read "not run: video not supplied".
- 2026-10-22 P0 env: 4 CPU, 15 GB RAM, ~30 GB disk, NO GPU, no Jetson, Docker CLI present. Reachable: github (public), pypi, pytorch CPU wheels, huggingface. Unreachable: pan.baidu.com (MAFS download). Constrained replay will be an approximation.
- 2026-10-22 P2 harness: metrics.py + run_replay.py + dummy adapter; 5/5 unit tests pass (pytest, /tmp/hv venv). TODO: sitl_bridge.py, plots.py, freeze.
- 2026-10-23 P1: no NIDAR video -> synthesising: SITL A>B>C>D @200m AGL (tools/sitl_mission.py), Esri z19 mosaic (benchmark-only, not committed), tools/render_video.py (1280x800 gray, global shutter, 60fps, blur+haze). SITL build in progress.
