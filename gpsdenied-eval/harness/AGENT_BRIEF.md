# Shared brief for solution agents (GPS-denied navigation evaluation)

Project: gpsdenied-eval/ (inside /home/user/Nexmesh, branch claude/new-session-peh13r). Seven GPS-denied navigation solutions are tested side by side on the SAME data, same metric code. You own ONE solution folder; do not touch other folders or harness/ (tell the lead if the harness needs a change).

## Rules
- Never fabricate a number; every number comes from a logged run saved under results/<solution>/<dataset>/<run-id>/. If a test cannot run, write why (error text, 3 attempts: alternate version / source / build).
- Isolated env per solution (python venv under solutions/<sol>/env, or Docker). Do not share packages. Use `export PIP_CACHE_DIR=/tmp/pipcache`. CPU ONLY (no GPU, no Jetson; 4 cores shared with other agents: limit torch/OpenCV to 2 threads: OMP_NUM_THREADS=2, torch.set_num_threads(2), and run heavy jobs with `nice`). Disk is tight (~25 GB free in total): keep your env + caches < 6 GB, delete wheels/caches you do not need.
- Pin and record: commit hash (solutions/<sol>/COMMIT.txt exists, repo/ already cloned at that hash), package versions (pip freeze -> solutions/<sol>/lock.txt), model weights + source + size, seeds, CPU model.
- Record setup effort: wall-clock minutes from clone to first working output, and count of failed steps -> SETUP.md (exact commands that worked + every failure and fix).
- Licences: record each component's licence. Internal evaluation only. Do not redistribute weights/code/map tiles. Don't commit repo/, env/, weights, big files (.gitignore handles repo/ and env/); commit code, SETUP.md, small logs/CSVs only. Commit + push your own work with `git add <your folder> results/<sol>` then `git commit` and `git push` (retry on conflict with `git pull --rebase`). Commit messages end with:
  Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_017cT9f7LSzgVGMCpvfxtkvZ
- Append progress to STATUS.md only via one-line appends (`echo "- date Pn <sol>: action -> outcome (log path)" >> STATUS.md`), never rewrite it.
- Report negative results honestly (drift, failure, underperformance are valid findings; keep evidence: log excerpt, plot, failing frame).

## Data (all under data/nidar/ -- SYNTHETIC "NIDAR-like" data we generated; label every result "synthetic NIDAR-like")
- Flight: ArduCopter SITL A->B->C->D at 200 m AGL, 12 m/s, 152.8 s of cruise. Path ~1.4 km. Home near 18.5177N 73.8539E (Pune, India).
- Video: data/nidar/video.mp4 (1280x800 gray, global shutter, 60 fps, HFOV 60 deg, f=1108.5 px, GSD 0.18 m/px). camera.json has intrinsics.
- Frames: data/nidar/frames/<t>.png, t = seconds from cruise start, 5 Hz (764 frames, 8-bit gray PNG, 1280x800). Use `cv2.imread(path)` (BGR 3-channel) unless you need gray.
- truth.csv (5 Hz): t,lat,lon,alt,yaw(deg, 0=N clockwise). truth60.csv adds roll,pitch (rad) at 60 Hz. imu.csv: SITL RAW_IMU (t, accel m/s2, gyro rad/s; ~50 Hz) in the same time base.
- weather.json: blur episodes and haze episodes (seconds). Frames inside them are harder; failure analysis should use them.
- Reference map: data/nidar/map_esri_z19.png (6656x6656 BGR, Esri World_Imagery z19, 0.283 m/px ground at centre, Web-Mercator), georef via harness/geo.py MapRef(data/nidar/map_esri_z19.json): latlon_to_px / px_to_latlon / ground_m_per_px. GeoTIFF: data/nidar/map.tif (EPSG:3857, 3 band). IMPORTANT: Esri tiles are benchmark-only; never copy them outside the workspace. NOTE: the video was rendered from the SAME map, so map-vs-query appearance gap is optimistic -- say so in your results.
- Hints available to adapters: barometer height (truth alt), IMU attitude / compass yaw (truth yaw, roll=pitch~0). Pass hints only to solutions that natively use them.
- Flight timing in SITL for context: leg lengths A->B 500 m N, B->C 500 m E, C->D 250 m W + 400 m S.
- Fallback datasets (MAFS, MUN-FRL, ALTO) are NOT available (Baidu blocked; links unknown). Where your test plan says "fallback dataset", record "not run: dataset unreachable" unless you find an open mirror you can fetch (try, record the attempt).
- Replay on CPU is slow: run offline replay at 5 Hz frames (764) for the main test; use every 5th frame (1 Hz, 153 frames) for ablations/sweeps and say so. Latency/real-time/constrained tests must run ALONE, so do NOT run them: only implement them; the lead runs them serially afterwards (tell the lead the exact command).

## Harness (frozen API, read harness/schema.md, harness/run_replay.py, harness/metrics.py)
Adapter file solutions/<sol>/adapter.py exposing class Adapter with:
  init(self, map_path:str, config:dict)  -> load models/index (map_path = data/nidar/map_esri_z19.png; georef via geo.MapRef(map json next to it))
  step(self, frame:np.ndarray(BGR), timestamp:float, hints:dict|None) -> dict(lat, lon, alt|None, yaw|None, conf:float, accepted:bool)
  Return accepted=False (never a guess) when no fix. Extra keys allowed (e.g. n_inliers, raw_lat, raw_lon, vio_lat, vio_lon, flag) -- add them to the returned dict, and write per-frame extras yourself to results if needed.
  hints = {"baro_alt": m AGL, "att": (roll, pitch, yaw_rad)} or None.
Run: `python harness/run_replay.py --adapter solutions/<sol>/adapter.py --frames data/nidar/frames --truth data/nidar/truth.csv --map data/nidar/map_esri_z19.png --out results/<sol>/nidar/<run-id> --mode offline --seed 0` (needs numpy, opencv, so run it with YOUR env after `pip install numpy opencv-python-headless`; the script is plain python). It writes estimates.csv (t,lat,lon,alt,yaw,conf,accepted) and run.json (latency_ms list, peak_rss_mb, startup_s, dropped). For extra keys or subsampling, write your own thin driver script next to the adapter that imports harness/run_replay.py functions (run(adapter, frames_dir, truth, map_path, out_dir, ...)); keep the SAME metric code: `harness/metrics.py: summarize(est, truth, n_frames)` (est dict of arrays with t,lat,lon,accepted; truth dict from run_replay.load_truth). Write results/<sol>/nidar/<run-id>/metrics.json from it.
Run IDs: <testid>-<yyyymmdd>-s<seed> (e.g. S1-T1-20261023-s0).
For SITL (scenarios SIM-0..7) you do NOT run SITL; the lead will, using your precomputed estimates.csv from the full 5 Hz offline replay (T1/primary test). Make sure that file exists for your solution.

## Deliverables per solution (write to your folder, commit, push)
1. SETUP.md (commands, failures, time, reproducibility verdict, licence table, documentation-quality rubric 0-3 each for: install steps work as written; config explained; results reproducible; algorithm described).
2. adapter.py + driver, lock.txt, COMMIT.txt (exists), weights source/size note.
3. results/<sol>/nidar/<run-id>/{estimates.csv,run.json,metrics.json,log.txt} for each test run.
4. ANALYSIS.md: plain-language explanation of how it works written from the code actually run (not copied from the README); data-flow architecture as a list of stages naming models/libraries (the lead will draw it); results table per test ID (metrics from harness metrics.py); failure analysis with >=5 frames/time windows where it failed (give frame times t, cause; use weather.json windows) and, if possible, save the failing frame, the expected map crop and the matched crop as PNGs under results/<sol>/figures/failures/; comparison with the author's claims (reproduced / partly / not, numbers side by side; remember our data is synthetic and altitude/camera differ); verdict (strengths, weaknesses, when to use / not use); known risks observed.
5. A short final message to the lead listing: tests done / not run (with reasons), commands for the lead's serial latency tests (real-time + constrained), where outputs are, anything harness-related that blocked you.
