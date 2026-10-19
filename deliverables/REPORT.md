# Floor 18 drone video → textured model: report (v3)

**Result:** the whole 664 s flight is registered (1,829 of 1,993 three-fps frames, 92 %), aligned to the Blender model and baked into a 4096² texture. **53.5 % of the model's surface area (1,348 of 2,520 m²) has real texture.** Poses up to frame #1540 (8:34, 514 s) were verified against the model and are unchanged. **v3 fixes the camera "jumping" after 8:34**: the SfM model had joined the last ~150 s onto the earlier trajectory with the wrong place and scale (a teleport of 82 m/s at frame 1545, and two more at 1642 and 1832). The late section is now re-tracked as three separate pieces, each placed by its own similarity transform and checked frame by frame; the trajectory is continuous (fastest step 2.0 m/s) but its accuracy is only moderate (see §4).

Your prompt still had unfilled placeholders (`[CAMERA NAME]`, `[RESOLUTION AND FPS]`, `[METERS / NOT SURE]`, no measured distance). I used the defaults below; the camera in the file is named `Camera`.

## What was blocking v1, and how it was resolved
v1 stopped at frame 898 (≈ 299 s). Frames 935–947 are a close-up of a flat, dim, uniform wall (no features for any matcher), followed by heavy motion blur, so frame-to-frame SIFT matching could not cross them.
1. **Vocabulary-tree retrieval (COLMAP's `vocab_tree_faiss_flickr100K_words256K.bin`).** Matching every frame against its 40 most similar frames found revisits: the drone saw the same places earlier (for example frame 952 matches frames 558–570 with 359/312/239 correspondences). That joined the match graph into one component (2,125 of 2,263 images at ≥ 30 inliers, up from broken pieces) and COLMAP then registered the post-gap frames.
2. **SuperPoint + LightGlue, with 3D taken from the Blender model** (script 11/14). Frame-by-frame chaining localised frames 899–934 with hundreds of inliers, but also failed on the blank-wall frames 935–947, which confirms that stretch has no usable features. The same machinery was used for drift correction (below).
3. Failed or abandoned: blind 2D alignment of fragments to the model (it picked the wrong scale even on the known model), brute-force relocalisation by itself (only 32 inliers), my first segment-wise ICP refinement (made alignment worse; see v1).

## 1. Inspect
| Item | Finding |
|---|---|
| Objects | 806 flat quads (451 wall, 92 column, 84 door, 173 window, 6 railing), 1,672 triangles, 1 camera. No floor, ceiling or window glass. |
| Bounding box | x −19.41…16.19, y −12.74…12.92, z 0…3.025 → 35.60 × 25.66 × 3.025 m |
| Units | Scene scale 1.0, metric, display unit mm. Geometry is plausibly **metres** (wall height 3.025). No measured distance was supplied, so metres is an assumption. |
| UVs | 800 of 806 objects have UVs. They tile at 1 UV unit = 1 m, so they are not an atlas. Kept as `UVMap`; new `BakedUV` layer added (active render). |
| Anchor camera | location (−7.4702, 13.0614, 1.6307), rotation XYZ (88.382°, 0, −178.235°), lens 14.68 mm / sensor 36 mm (horizontal FOV 101.6°), 1920×1080 |
| Video | 3840×2160 HEVC, 29.97 fps, 664.4 s, 19,912 frames, no rotation flag, `EIS:RS; FOV:Linear` |

SfM gives fx ≈ 1088 px at 1920 wide (horizontal FOV ≈ 83.8°), which disagrees with the anchor's 101.6°, so the anchor was used for pose only.

## 2. Frames
3 fps, 1920 px wide: 1,993 frames, plus 10 fps frames in the 295–325 s window. The variance-of-Laplacian filter kept 1,564 and dropped 429, but dropping frames broke matching links (27.6 % of links across a dropped frame had no verified matches against 3.8 % between adjacent frames), so **all frames were used**. See `evidence/contact_sheet.jpg`.

## 3. Camera poses (pycolmap 4.2.1, CPU)
Single shared `OPENCV` camera at 1920×1080: fx 1087.8, fy 1085.3, cx/cy 960/540 (fixed), k1 −0.0030, k2 0.0052, p1 0.00017, p2 0.00068. Almost no distortion, as expected from `FOV:Linear`.
- Final model: **2,069 registered images (1,829 of 1,993 three-fps frames), mean reprojection error 0.60 px, 407 k points**, spanning 0–664 s.
- Frames still not registered (short runs, blank wall or blur): 248–254 s, 311–314 s, 404–411 s, 465–467 s, 500–503 s, 553–555 s, 585–591 s, 614–620 s, plus scattered single frames.

## 4. Alignment to Blender
- Sim3 anchored at the first-frame camera and composed with the checked alignment of the first model: **2.381 m per COLMAP unit**. The first 865 cameras of the old and new model agree to a uniform scale (0.73) with a 1.4 cm median residual.
- Verified visually on overlays through frame ≈ 1346 (`evidence/verify_m0.jpg`, `evidence/verify_ext3.jpg`): door, window, column and stair edges land within roughly **5–15 px at 1920 wide (≈ 1–4 cm at 3 m)**, an eyeball estimate. The stairwell at 926, the F-18 corridor at 973 and the window at 1117 all match.
- **What went wrong after 8:34 (v2):** the SfM trajectory teleported at frames 1545 (82 m/s), 1642 (13 m/s) and 1832 (3.9 m/s). None of the late frames has a direct match (≥ 30 inliers) to a verified frame, so those pieces were attached to the model by weak or spurious links and sat in the wrong place with the wrong scale. My v2 correction only bent the misplaced pieces, which left jumps.
- **Re-tracking (v3, scripts 16 and 20):** the trajectory is split at those teleports into three pieces. Each piece gets its own scale (0.93, 0.70, 0.87 relative to the first model's units) and a local rigid correction, fitted to model-anchored localisations (SuperPoint + LightGlue matches to already-placed frames, PnP on 3D points ray-cast from the model). Frames are processed in order of time-distance from frames already placed, in both directions, starting from the verified frames at 1541 and one hand-checked seed at frame 1822 (a pose I chose by looking at the frame and the plan: the drone is in the west end of the long corridor looking east). 308 frames were localised directly, 88 failed and take their pose from the piece fit. Camera roll is levelled (the gimbal is stabilised).
- **Checks I did by eye** (`evidence/s3.jpg`, `s4.jpg`, `plan_sd1.jpg`): frames 1545, 1560, 1574, 1605, 1650, 1712, 1735, 1754, 1880, 1900 line up well (the window openings at 1880/1900 match exactly); 1836 and 1862 are off by about 3–4° of heading; 1638, 1700 and 1843 are off by roughly 50–100 px (about 15–30 cm or a few degrees); 1925, 1950, 1975 and 1992 are flat walls or floor with too few model edges to judge. The top-down path is continuous and plausible: west along the corridor, the south-west rooms, back east along the corridor, into the big central room, then south to the door near the south wall.
- **Tried and rejected:** an automatic per-frame edge-alignment optimiser. It improved its own score but made the overlays no better (in 1638 it simply pushed most edges out of view), so it was not applied.
- **Remaining accuracy in the late section is moderate:** roughly 15–30 cm and 3–5° in places, not verified frame by frame.

## 5. Pose refinement
- v1 segment-wise ICP: rejected (worse overlays).
- v2 PnP drift correction was replaced in v3 by the piece-wise re-tracking above (frames ≥ 1541). Automatic edge refinement was tried and rejected.

## 6. Texture bake
- Atlas: 4096², **1.18 cm/texel**, covering the 1,087 of 1,672 triangles some camera saw; never-seen triangles share a flat patch.
- Per texel: project into 1,883 cameras, embree depth-map occlusion test (7 cm + 3 %), weights for view angle, distance, sharpness and image edge, exposure gains (two iterations, clamped 0.7–1.4), two-band blend (broad weights for low frequencies, sharp for detail), 4 px gutters. This is not true multi-band or Poisson blending.
- **Tier A** = frames up to #1540 (full weight, verified poses). **Tier B** = frames from #1541 (re-tracked, continuous, moderate accuracy), weighted ×0.6.
- `coverage_mask_4096.png`: **255 = covered by Tier A frames, 128 = covered only by Tier B frames (moderate pose accuracy), 0 = no data** (flat grey in the texture).
- **Coverage: 1,348 m² of 2,520 m² = 53.5 %** (surface area counts both sides of each wall). Of this, **1,076 m² (42.7 %) has Tier A support**; the remainder comes only from Tier B frames and may be misaligned by 15–30 cm.
- End-to-end check: the delivered `.blend` rendered from its own animated camera reproduces the footage at 12 frames (`evidence/compare_late.jpg` for the late section). Late frames reproduce the layout (corridor and far windows at 1560, window room at 1650, window with sunlit wall at 1880/1900), but flat walls such as 1950 come out smeared, and window and scaffold areas are smeared.

## Known problems
1. **Tier B poses (after 8:34) are continuous but only moderately accurate** (15–30 cm / 3–5° in places); texture from them can be misaligned or ghosted. Treat the grey (128) mask areas with caution, or bake with Tier A only.
2. Eight short unregistered runs (see §3) and the blank-wall stretch at 311–314 s have no pose.
3. The model has no floor, ceiling or window glass, so those are empty in renders. Windows and doors seen from inside show the outdoors, scaffolding and netting.
4. Anything in front of a wall during the flight (scaffold poles, tape, people, a hard hat) is baked into the wall texture. Direct sunlight patches are averaged, so lighting differs between views.
5. Banding and blur: some wall islands show vertical bands where the best view switches, and grazing-angle areas are blurry. Nearest columns in some views are streaky.
6. 1,087 of 1,672 triangles were seen; ~46 % of the surface remains flat grey (mostly back faces never visible from inside, or areas flown past quickly).
7. Texture used 1920 px frames. For 4K, re-extract full-resolution frames and re-run `08_bake.py` with `frame_scale=2`.
8. Pose accuracy was judged visually, not against surveyed points.

## Files
- `Floor18_textured.blend` — model with `BakedUV` and material `Floor18_BakedTexture`, plus camera `DroneCam_estimated` keyframed for 1,909 frames (tier recorded per frame in the pose files) (lens 20.4 mm on a 36 mm sensor).
- `texture_4096.png`, `coverage_mask_4096.png`, `bake_stats.json`, `exposure_gains.json`, `atlas_uv.npz`
- `camera_poses_blender.json` / `.csv` — per-frame Blender `matrix_world`, location, euler, time (s), tier (A/B), intrinsics.
- `sim3_colmap_to_blender.json`, `poses_internal_bake_input.json`
- `scripts/` (see `scripts/README.md`) and `evidence/`
