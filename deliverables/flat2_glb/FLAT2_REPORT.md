# Flat 2 GLB textures → Floor18 model (Flat 2 and its mirror, Flat 1)

## What was done
1. **Identified the flats by geometry.** The GLB's wall layout (rotated/mirrored 8 ways and slid over your updated model's walls) fits best unrotated at an offset of (−7.17, −3.06, +0.12) m. That is the **north-west flat on the west side** (x −19.5…−8.9, y −1.9…9.5): the 3BHK type with living/dining, three bedrooms, kitchen, lobby, balcony and three toilets, matching the PART-2 flats of the Wing B plan. The next-best fit is the same GLB **mirrored across y ≈ −1.61 m** (the corridor centre line), which lands exactly on the **south-west flat** (x −19.5…−8.9, y −12.7…−1.3). That is Flat 1. Overlay: `evidence/flat_identification_plan_overlay.jpg` (red = Flat 2, blue = mirrored Flat 1, grey = your model). The +0.12 m lift puts the GLB floor 12 cm above the structural slab and its wall tops at the 3.025 m ceiling.
   I could not read the flat numbers off the PDF; "Flat 2 = north-west, Flat 1 = south-west" follows from the geometry match and from your statement that Flat 1 is the inverted twin.
2. **Wall textures.** 261 wall objects inside the two flats (Front/Back quads, columns, header/sill pieces) were moved to a new 4096² atlas (`flat_glb_atlas_4096.png`, 1.0 cm/texel) with a new `GLB_UV` layer and material `Flat_GLB_AtlasMaterial`. For each texel I ray-cast onto the aligned GLB (Flat 2) or its mirror (Flat 1) and copied the GLB's own texture at the hit (surfaces within about 0.5 m of the Floor18 face, facing the same way). Your drone atlas (`Floor18_baked`, `BakedUV`, material `Floor18_BakedTexture`) is untouched on all other objects.
3. **Floors.** Floor18 has no floor surfaces, so the GLB's floor meshes were added as 9 objects per flat (`Flat2_GLB_floors`, `Flat1_GLB_floors_mirrored`), with their GLB textures. Four floors have no texture in the GLB and are plain grey.
4. **Flat 1** uses the same textures mirrored across the plane, so marks and writing are mirrored.

## Numbers (`transfer_stats.json`)
7.63 M texels in the 261 objects: **5.69 M (74.5 %) from the GLB** (Flat 2 2.62 M, Flat 1 3.07 M), 0.89 M (11.7 %) keep the drone texture, 1.06 M (13.8 %) have no data (flat grey). `flat_glb_coverage_4096.png`: 255 = GLB, 160 = drone texture, 60 = no data.

## Checked
The updated model rendered from the same cameras as the GLB (5 views per flat) reproduces the GLB's wall textures with features in the same places in both flats (`evidence/compare_*`).

## Known limits
- The GLB has 9 materials with no texture (e.g. `Bedroom_3_Wall_S`, `Toilet_Bed3_Wall_E`, `Toilet_Common_Wall_E`); those areas stay drone texture or grey.
- The GLB models only room-side surfaces. Facade and outside faces, and partition walls the GLB lacks (for example `Wall_100mm_118`, `Wall_175mm_000`), have no GLB texture. Where the drone saw them they keep that; otherwise grey.
- Placement is good to roughly ±5 cm, so textures can sit a few cm off at edges. A wider search window (0.5 m) can attach a neighbouring wall's texture where the two models differ by that much.
- Window and door openings have no glass in either model (they render black/grey).
- Where both the drone and the GLB had texture, the GLB won.
- Your edits are preserved: the 60 deleted objects stay deleted, `Plane`, the empty `01_Slab` and `06_Stairs` collections and the animated `DroneCam_estimated` are untouched.

## Files
`Floor18_textured_Flat1_Flat2_glb.blend`, `flat_glb_atlas_4096.png`, `flat_glb_coverage_4096.png`, `transfer_stats.json`, `per_object.json` (per-wall breakdown), `placement.json`, `scripts/` (`match_flat.py` flat identification, `transfer_glb2.py` the transfer, `render_views.py` checks), `evidence/`.
