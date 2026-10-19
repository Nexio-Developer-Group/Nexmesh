# photogrammetry_output.glb: Floor18 model with photogrammetry/SLAM-style imperfections

## What was done
1. **Copy first.** `Floor18_textured_Flat1_Flat2_glb_ground.blend` was copied to `Floor18_photogrammetry_copy.blend`; all effects were applied to the copy. The original file is untouched.
2. **Effects applied to every wall, floor and the whole file** (`photogrammetry_effect.py`, seed 20261020, all parameters in `photogrammetry_params.json`):
   - **Subdivision.** The walls were single quads, so each was subdivided to about 14 cm edges (floors 35 cm) so that they can bend. UVs are kept.
   - **Whole-file drift** (SLAM-style): a yaw twist that grows along x (0.4° over 18 m), 0.25 % scale error, and a 1.5 cm bowl. This is the largest term: RMS 7.7 cm, up to 17.6 cm at the far corners.
   - **Bowing** at metre scale (3–9 m wavelengths, 2.9 cm RMS) and **waviness** (0.4–1.6 m, 0.8 cm RMS), plus **3 mm vertex noise**. All of these are functions of world position, so the Front and Back surfaces of a wall move together instead of tearing apart.
   - **Per-wall misregistration:** each wall gets its own small random tilt (about 0.25°) and offset (about 0.8 cm).
   - **Missing patches:** 525 irregular holes (8–35 cm) plus scattered speckle, 13,496 faces removed in total, as in unobserved regions.
   - **Texture imperfections:** per-wall exposure, contrast and tint shifts; ghosting (double images) on about 30 % of walls; 150 blur patches per atlas; film grain; a 2 px random UV shift per wall (texture swim); a smooth exposure field and blur patches on the ground; grain on the GLB floor textures.
3. **Exported** as `photogrammetry_output.glb` (binary glTF, textures as JPEG quality 72, no cameras or animation).

## Numbers
- Vertex displacement over the whole file: RMS 7.5 cm, 95th percentile 13.1 cm, maximum 20.3 cm.
- Faces: 24,374 original, 556,107 after subdivision, 542,611 in the GLB after holes. 730 meshes, 1.63 M vertices, 8 textures (the drone atlas, the GLB-flat atlas, the ground texture and five floor textures). 728 of the 730 meshes are textured; the other two (the loose `Plane` and one more) never had a texture.
- Texture islands modified: 367 in the drone atlas, 248 in the flat atlas.

## Checked
- The GLB reloads cleanly (730 meshes, correct bounds, textures present).
- Renders of the copy next to the clean model from the same drone cameras (`evidence/clean_vs_photogrammetry_drone_views.jpg`) show the holes, the bowing and shifting of walls, the exposure and tint changes, ghosting and grain.
- `evidence/systematic_distortion_field.jpg` plots the systematic part of the distortion over the plan.

## Notes and limits
- The mauve or pink patches in some renders are not new. They are the atlas's existing "no data" colour for faces the drone never saw, and they appear in the clean render too.
- This is a synthetic imitation of reconstruction errors (drift, bowing, noise, holes, exposure/seam artefacts), not the output of a real SLAM/photogrammetry run. Lens-style distortion is represented geometrically (twist, scale, bowl and waviness); no camera lens model is applied to the images.
- The distortion is deterministic for a given seed; change `seed` (or any value in the parameter dictionary at the top of the script) to generate other variants.
- The GLB is large (74.5 MB) because the subdivided walls carry 543 k faces and three 4096² textures.

## Files
`photogrammetry_output.glb`, `Floor18_photogrammetry_copy.blend`, `photogrammetry_params.json`, `photogrammetry_effect.py`, `evidence/`.
