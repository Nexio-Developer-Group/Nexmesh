# 100-500 m altitude check

Short clear-weather clips of the same path at fixed heights above ground (stabilised nadir camera, 60 deg HFOV, 1280 x 800, 30 frames each: one every 5 s along the whole flight, so all headings), made by `tools/make_altitude_clips.py`. S6 with **global search only** (`tracking: false`, the harder case: every frame is localised from scratch), default tile scales (75, 110, 165, 250, 380, 580 m), CPU.

| Height above ground | frames | fixes | fix rate | median error | p95 | max |
|---|---|---|---|---|---|---|
| 100 m | 30 | 20 | 66.7 % | 1.05 m | 2.77 m | 2.88 m |
| 200 m | 30 | 27 | 90.0 % | 0.44 m | 1.44 m | 5.19 m |
| 300 m | 30 | 28 | 93.3 % | 0.46 m | 1.47 m | 2.13 m |
| 400 m | 30 | 29 | 96.7 % | 0.66 m | 1.64 m | 1.94 m |
| 500 m | 30 | 30 | 100 % | 0.82 m | 1.33 m | 2.24 m |

Reading it:
* The image is scaled by the height you give it, and the retrieval index has tiles of several sizes, so the same code covers 100-500 m. A fix, when accepted, was accurate at every height (the map and the image are the same imagery here, so absolute accuracy is optimistic).
* **100 m is the weak end** (a third of frames without a fix): the image covers only 115 x 72 m, with few distinctive features per frame. Tracking (`engines.s6.tracking: true`, default) fills such gaps from the previous fix; it was not part of this sweep. A rougher map or real texture changes these numbers.
* An earlier run with only two tile scales (145 and 290 m) gave 67 % at 300 m; the finer scales raised it to 93 %. If you fly one altitude, keep two scales around it; if you fly a range, keep the default list.
* Wrong height is the main way to break S6: its gate rejects a fix whose height differs from the altitude source by more than 10 % (min 12 m). Set `telemetry.altitude.offset_m` for terrain.
* S5 was **not** swept (it is slower, and its evaluation covered 200 m only). Its altitude handling pins the particles to the telemetry altitude (`altitude_from_telemetry`) and scales the image crop with it, which is designed for 100-500 m but untested here.
* Heights above 500 m: the largest tile scale is 580 m; add 800-1000 m if needed (cost: index time, MegaLoc input size).
