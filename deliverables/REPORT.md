# Floor 18 drone video → textured model: report

**Result in one line:** the first ~5 minutes of the flight (video frames 0–299 s, 865 camera poses) are registered to the Blender model and baked into a 4096² texture. The remaining ~6 minutes could not be registered. 19.3 % of the model's surface area has real texture.

Your prompt still had unfilled placeholders (`[CAMERA NAME]`, `[RESOLUTION AND FPS]`, `[METERS / NOT SURE]`, no measured distance). I used the defaults below. The camera in the file is named `Camera`.

## 1. Inspect
| Item | Finding |
|---|---|
| Objects | 806 mesh objects (451 wall, 92 column, 84 door, 173 window, 6 railing), 1672 triangles, 1 camera. Every object is a flat quad, so there is no floor, ceiling or window glass. |
| Bounding box | x −19.41…16.19, y −12.74…12.92, z 0…3.025, i.e. 35.60 × 25.66 × 3.025 m |
| Units | Scene unit scale 1.0, metric, display unit mm. Geometry is plausibly in **metres** (wall height 3.025). I did not have a measured distance, so metres is an assumption. The Sim3 scale comes from model geometry. |
| UVs | 800 of 806 objects have UVs. They tile at 1 UV unit = 1 m (UV area = 3D area, ranges up to 4.2). They are not a 0–1 atlas, so they cannot hold a texture. **I kept them untouched as `UVMap` and added a new `BakedUV` layer (active render).** The 6 objects with no UVs also got `BakedUV`. |
| Anchor camera | location (−7.4702, 13.0614, 1.6307), rotation XYZ euler (88.382°, 0, −178.235°), lens 14.68 mm, sensor 36 × 24 mm (horizontal FOV 101.6°), render 1920×1080 |
| Video | 3840×2160 HEVC, 29.97 fps, 664.4 s, 19 912 frames, no rotation flag. Tag `EIS:RS; FOV:Linear`. A `djmd` metadata track exists (I did not decode it). |

**Anchor lens mismatch:** SfM gives fx ≈ 1088 px at 1920 wide, a horizontal FOV of ≈ 83.8°. The anchor camera's 101.6° does not match. I used the anchor camera only for its pose.

## 2. Frames
Extracted at 3 fps, 1920 px wide: 1993 frames. Variance-of-Laplacian filter (relative to a ±15-frame median) kept 1564 and dropped 429. **The filter hurt**: 27.6 % of links across a dropped frame had no verified matches, against 3.8 % between truly adjacent frames. All frames were therefore used, plus 300 extra frames at 10 fps in the 295–325 s window. See `evidence/contact_sheet.jpg`.

## 3. Camera poses (pycolmap 4.2.1, CPU)
Single shared `OPENCV` camera, focal prior from the anchor lens.
- First attempt with the blur-filtered frames fragmented into dozens of components.
- After adding all frames back, mapping produced one large model plus many small fragments.
- **Main model: 826 frames, video frames 1–898 (0–299 s), mean reprojection error 0.52 px, 259k points.** With the dense extras, 865 frames at 0.53 px.
- Intrinsics at 1920×1080: fx 1087.8, fy 1085.3, cx/cy fixed at 960/540, k1 −0.0030, k2 0.0052, p1 0.00017, p2 0.00068. The lens is nearly distortion-free, consistent with `FOV:Linear`, so OPENCV_FISHEYE was not needed.
- **Failure:** nothing registers past frame 898. At 899–961 the drone goes from a blank-wall close-up into a dark stairwell with heavy motion blur (Laplacian variance 20–60). Even at 10 fps the sequence cannot bridge it. The later frames form small fragments (largest: 245 frames, 962–1485 in 3 fps index) that are not linked to the main model.
- Things I tried: lower thresholds, all frames, 10 fps densification, and aligning the fragments blindly to the model with a gravity-plus-2D-search method. The blind alignment recovered the wrong scale even on the known model and was abandoned.
- **Suggested fixes:** re-shoot or trim around the stairwell, fly it slower with more light, or add a loop-closing pass. If you can supply a SIFT vocabulary tree, that enables loop detection. Running on GPU at full 4K may also bridge the gap.

## 4. Alignment to Blender
- Sim3 anchored to the first-frame camera pose, then scale and small corrections fitted on 30 000 SfM points against the model's walls: **scale 1.743 m per COLMAP unit; median point-to-model distance 8.2 cm** (mean is much larger because of windows, scaffolding and other clutter not in the model). The fit moved the anchor by 3.1° and 0.20 m, so the anchor pose is not exact.
- Overlays at 6 frames across the flight (`evidence/verify_m0.jpg`, `evidence/compare.jpg`, `evidence/compare2.jpg`): the door, window and wall edges align to roughly **5–15 px at 1920 px wide, about 1–4 cm at 3 m range**. This is an eyeball estimate, not a rigorous measurement. Frame 770 is visibly offset more.
- Per-segment point-to-model median: 1–8 cm for most segments, 16–17 cm around frames 100–200 and 500–520, and 40 cm near 593–622 (blank wall, few reliable points).

## 5. Pose refinement
I tried piecewise point-to-plane refinement. It produced rotation corrections up to 24° in under-constrained segments and **made the overlays visibly worse** (`evidence/cmp.jpg`, unrefined on the left of each pair). I rejected it. The delivered poses are the unrefined Sim3-aligned SfM poses.

## 6. Texture bake
- New atlas: 4096², 0.80 cm/texel, only the 431 of 1672 triangles that some camera saw. Unseen triangles share one flat patch.
- Per texel: project into 865 candidate cameras, occlusion test with an embree depth map (1/6 scale, 7 cm + 3 % tolerance), weight by view angle, distance, sharpness and distance from the image edge, per-frame exposure gain (two iterations, clamped to 0.7–1.4), then a two-band blend (broad weights for low frequencies, sharp weights for detail) with 4 px gutters. Not true multi-band or Poisson blending.
- Never-seen texels are flat grey (110,110,120). `coverage_mask_4096.png` is white where there is real data.
- **Coverage: 485.5 m² of 2520.2 m² of surface = 19.3 %** (area counts both sides of each wall). Atlas pixels with real data: 45.4 %.
- Verification: the delivered `.blend` rendered from its own animated camera matches the video frames at 7 frames (`evidence/compare*.jpg`).

## Known problems
1. Only the first ~45 % of the flight is registered; the rest of the floor is untextured.
2. The model has no floor, ceiling or window glass, so those are empty in renders.
3. Anything in front of a wall during the flight (scaffold poles, red/white tape, the worker, a hard hat) is baked into the wall texture. Some of it is smeared.
4. Banding and blur: a few wall islands show vertical bands where the best view switches, and grazing-angle areas are blurry.
5. Windows and doors seen from inside show the outdoors bright; surfaces with no data are flat grey.
6. Texture used 1920 px frames. For 4K, re-extract at full resolution and run `08_bake.py` with `frame_scale=2`.
7. Pose accuracy was judged visually, not against surveyed points.

## Files
- `Floor18_textured.blend` — model with `BakedUV` and material `Floor18_BakedTexture`, plus camera `DroneCam_estimated` keyframed for 865 frames (lens 20.4 mm on a 36 mm sensor).
- `texture_4096.png`, `coverage_mask_4096.png`, `bake_stats.json`, `exposure_gains.json`, `atlas_uv.npz`
- `camera_poses_blender.json` / `.csv` — per-frame Blender `matrix_world`, location, euler, time in seconds, intrinsics.
- `sim3_colmap_to_blender.json`, `poses_internal_colmap_aligned.json`
- `scripts/` and `evidence/` — see `scripts/README.md` for the run order.
