# Output reference

Every run creates `runs/<YYYYmmdd-HHMMSS>_<name>/`. Column meanings:

**estimates_<engine>.csv** (one row per frame the engine processed)
`t` frame time (s; live = monotonic clock, replay = recorded time) | `wall_utc` | `engine` | `accepted` (1 = a fix was produced and passed the engine's own checks) | `lat, lon` absolute position of the drone calculated by the engine (empty when not accepted) | `alt_est_m` height the engine estimated | `yaw_est_deg` heading it estimated | `conf` 0-1 | `latency_ms` | `raw_lat, raw_lon` best candidate even when rejected | `info` JSON of engine details (S6: mode global/track, inliers, flags, tile scale; S5: particle count, spread, converged) |
`gps_lat, gps_lon, gps_fix, gps_age_s` real GPS used as reference | `err_north_m, err_east_m, err_m` estimate minus GPS | **`corr_north_m, corr_east_m, corr_lat_deg, corr_lon_deg`: the correction that moves the estimate onto the GPS (GPS minus estimate)**.

**fused.csv** (when two engines run): `source` (s6, s5, s6+s5), `quality` (`single` one engine had a fix, `agree` both within `fusion.agree_radius_m` and averaged, `conflict` both had fixes but disagreed: the primary or the clearly more confident one is output), `disagree_m`, plus the same GPS comparison columns.

**frames.csv**: per camera frame, the telemetry used (`alt_agl_m`, attitude, velocity), whether it was usable (`hints_ok`, `reason`), which engines received it (`offered_to`), and the GPS at that time. A frame that no engine processed because it was busy is `offered_to` it but has no row in its estimates file (counted as `dropped_busy` in the summary).

**telemetry.csv**: MAVLink telemetry at `output.telemetry_log_hz` (live runs). **summary.json / summary.md**: fix rate, error statistics against the GPS (median, p95, max, shares within 10/50/100 m), mean correction (north/east), latency, dropped frames, fused quality counts.

Compare runs: `nexnav summarize <run_folder>` recomputes the statistics from the CSVs.
