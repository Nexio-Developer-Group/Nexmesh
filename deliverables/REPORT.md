# Floor 18 drone video → textured model: report (v2)

**Result:** the whole 664 s flight is now registered (1,829 of 1,993 three-fps frames, 92 %), aligned to the Blender model and baked into a 4096² texture. **53.1 % of the model's surface area (1,339 of 2,520 m²) has real texture.** Poses for the first ~513 s (frames up to #1540) were verified visually against the model. Poses for the last ~150 s are drift-corrected and only good to roughly 10–30 cm, so texture that comes only from them is marked separately in the coverage mask.

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
- **Drift:** after ~1400 the SfM trajectory slides relative to the building (point-to-model median error rises from 4–11 cm in the first 250 s to 40–80 cm in the last 300 s; some of this is clutter, but overlays at 1569–1925 show empty or wrong edges, so it is real).
- **Correction for frames ≥ 1341:** every 2nd frame was localised against the model (SuperPoint + LightGlue, PnP on 3D points ray-cast from already-corrected reference frames), camera roll levelled (the gimbal is stabilised; native roll was ±1.5°), jumps over 1 m / 10° rejected, and the correction interpolated onto the SfM poses in between. 141 anchors, 16 rejected, 130 frames without a match. Median corrections were 3 cm at 1341–1440, 8 cm at 1441–1540, then 17–33 cm for 1541–1940, up to 0.8 m.
- **Remaining accuracy after correction is not good enough to call verified.** Overlays at 1569–1650 and 1925 are visibly off (tens of px, roughly 10–30 cm). I tried an edge-gradient score to flag bad frames, but it did not separate good from bad overlays, so I did not use it.

## 5. Pose refinement
- v1 segment-wise ICP: rejected (worse overlays).
- v2 PnP drift correction above is the refinement that was kept. It applies only to frames ≥ 1341.

## 6. Texture bake
- Atlas: 4096², **1.28 cm/texel**, covering the 1,168 of 1,672 triangles some camera saw; never-seen triangles share a flat patch.
- Per texel: project into 1,854 cameras, embree depth-map occlusion test (7 cm + 3 %), weights for view angle, distance, sharpness and image edge, exposure gains (two iterations, clamped 0.7–1.4), two-band blend (broad weights for low frequencies, sharp for detail), 4 px gutters. This is not true multi-band or Poisson blending.
- **Tier A** = frames up to #1540 (full weight, verified poses). **Tier B** = later drift-corrected frames, weighted ×0.35.
- `coverage_mask_4096.png`: **255 = covered by Tier A frames, 128 = covered only by Tier B frames, 0 = no data** (flat grey in the texture).
- **Coverage: 1,339 m² of 2,520 m² = 53.1 %** (surface area counts both sides of each wall). Of this, **1,076 m² (42.7 %) has Tier A support**; the other ~10.4 % comes only from Tier B and may be misregistered by 10–30 cm.
- End-to-end check: the delivered `.blend` rendered from its own animated camera reproduces the footage at 12 frames (`evidence/compare_a.jpg`, `compare_b.jpg`, and the earlier set). Tier-B frames show smeared window and scaffold areas.

## Known problems
1. **Tier B poses (last ~150 s) are approximate** (10–30 cm); texture from them can be misaligned or ghosted. Treat the grey (128) mask areas with caution, or bake with Tier A only.
2. Eight short unregistered runs (see §3) and the blank-wall stretch at 311–314 s have no pose.
3. The model has no floor, ceiling or window glass, so those are empty in renders. Windows and doors seen from inside show the outdoors, scaffolding and netting.
4. Anything in front of a wall during the flight (scaffold poles, tape, people, a hard hat) is baked into the wall texture. Direct sunlight patches are averaged, so lighting differs between views.
5. Banding and blur: some wall islands show vertical bands where the best view switches, and grazing-angle areas are blurry. Nearest columns in some views are streaky.
6. 1,168 of 1,672 triangles were seen; ~47 % of the surface remains flat grey (mostly back faces never visible from inside, or areas flown past quickly).
7. Texture used 1920 px frames. For 4K, re-extract full-resolution frames and re-run `08_bake.py` with `frame_scale=2`.
8. Pose accuracy was judged visually, not against surveyed points.

## Files
- `Floor18_textured.blend` — model with `BakedUV` and material `Floor18_BakedTexture`, plus camera `DroneCam_estimated` keyframed for 1,909 frames (lens 20.4 mm on a 36 mm sensor).
- `texture_4096.png`, `coverage_mask_4096.png`, `bake_stats.json`, `exposure_gains.json`, `atlas_uv.npz`
- `camera_poses_blender.json` / `.csv` — per-frame Blender `matrix_world`, location, euler, time (s), tier (A/B), intrinsics.
- `sim3_colmap_to_blender.json`, `poses_internal_bake_input.json`
- `scripts/` (see `scripts/README.md`) and `evidence/`
