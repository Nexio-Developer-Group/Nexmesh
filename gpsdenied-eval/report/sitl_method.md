**Method.** ArduCopter 4.5.7 SITL at 8x speed. Take-off to 200 m, then the A→B→C→D mission in GUIDED at 12 m/s. A second GPS (`GPS_TYPE2 = 14`, MAVLink) receives `GPS_INPUT` messages from `harness/sitl_bridge.py` (accepted fixes only; velocity, altitude and accuracy-of-speed fields ignored; horizontal accuracy from the solution's confidence; each fix projected forward to the send time with the EKF velocity). EKF3 is set to position-only GPS (`EK3_SRC1_POSXY=3`, `VELXY=0`, `POSZ=1` baro), `EK3_GPS_CHECK=0`. The real GPS is cut with `SIM_GPS_DISABLE=1` at mission time t = 60 s (t = 0 in SIM-6) and the second GPS becomes primary. A run **fails** when the EKF raises a failsafe that ends the mission (LAND) or the vehicle leaves the planned corridor by more than 200 m. Final parameter files, bridge logs, EKF-vs-truth traces and dataflash logs (seed 0, git-ignored) are in `results/<row>/sitl/<scenario>-s<seed>/`.

| ID | Scenario |
|---|---|
| SIM-0 | GPS stays on; solution only logged (fixes not sent) |
| SIM-1 | GPS cut at 60 s, no vision: dead-reckoning control |
| SIM-2 | GPS cut at 60 s, solution feeds `GPS_INPUT` (main test) |
| SIM-3 | as SIM-2, solution output dropped in 20 s windows (80-100 s and 125-145 s) |
| SIM-4a/4b | as SIM-2 with 200 ms / 500 ms added latency |
| SIM-5 | as SIM-2 with 5 % of fixes moved by 100-300 m |
| SIM-6 | GPS cut at the start of cruise (cold start; take-off cut not possible, see deviations) |
| SIM-7 | full mission with GPS cut at 60 s, then return-to-launch with no vision on the way back (the recorded video ends at D) |

Rows other than S1-S6: a **control** (ground truth plus 5 m noise as fixes) shows what the EKF achieves with ideal fixes; "S2 VIO-only" is the S2 odometry track without map fixes; "S3 + compass de-rotation" is our extension. These rows are not ranked.
