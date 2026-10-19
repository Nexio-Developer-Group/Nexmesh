# Ground surface for the whole floor, textured from the drone footage

## What was added to `Floor18_textured_Flat1_Flat2_glb_ground.blend`
- **`Floor_Drone_Ground`** (collection `01_Slab`): one floor surface of **721 m²** at z = 0, material `Floor18_DroneGround`, UV layer `GroundUV` (a plain top-down projection, so `floor_ground_texture.png` is a top-down photo-map of the floor, 1.25 cm/texel, 2884 × 2104 px).
- The 8 untextured grey GLB floor placeholders I added earlier (4 per flat) were **removed**; the drone floor shows there now. The 10 textured GLB floors were lowered to the same floor level (+4 mm, to avoid z-fighting), so each flat has one consistent floor.
- Everything else from your file (walls, both atlases, `Plane`, the empty `06_Stairs`, the animated drone camera) is unchanged.

## How it was made
1. **Floor level.** The SfM points of the drone footage show a sharp peak at z ≈ −0.01 m (about 9,700 points), i.e. the model's own z = 0 plane, so the ground is at z = 0. (The GLB floors sat 12 cm higher; they are now lowered to match.)
2. **Footprint.** Your current model has only part of the walls, so the floor plate was built from the wall traces closed morphologically, plus the area around the drone's flight line, trimmed to the closed rooms and a 0.35 m margin around what the drone saw.
3. **Texture.** For each floor texel I project it into every drone frame with the tracked poses, test visibility, and sample the colour. Visibility uses (a) depth maps of the *complete original* wall model, so removed walls still block, and (b) the SfM points the drone reconstructed (anything more than 25 cm above the floor in front of the texel blocks it). Views are weighted by angle, distance, sharpness and image edge, with per-frame exposure gains.
4. **Photometric consensus (important).** A drone at 1.3–1.7 m looking forward sees only a thin strip of floor at a shallow angle, and wall pixels get projected onto the floor wherever the model lacks the wall. A real floor point looks the same from different frames; a wall pixel smears differently in each. So each texel keeps only the samples that agree with its best view, and needs at least 2 agreeing views. This removed the long streaks of my first attempt, at the cost of coverage. Sky and blown-out pixels are also rejected.

## Numbers (`floor_stats.json`)
- Floor plate considered: 805 m². Mesh in the file: **721 m²**.
- **Textured by the drone: 350 m² (about 49 % of the mesh)**, of which 282 m² has verified-pose frames behind it (frames up to 8:34) and 68 m² only the later, less accurate frames.
- The rest (inside the mesh) is a flat concrete-grey fill (`fill_rgb` in the stats). In the two west flats the GLB floor textures cover the rooms that have them.
- `floor_ground_coverage.png`: 255 = verified-pose frames, 128 = late frames only, 90 = small gaps filled by inpainting, 60 = no consistent data (flat fill), 0 = outside the mesh.

## Checked
- Top-down render of the floors (`evidence/top_down_floor_render.jpg`) and renders from the drone's own camera at six frames next to the real footage (`evidence/drone_view_vs_render_with_ground.jpg`): debris at the foot of the window room (frame 150) and the dark wet patch on the corridor floor (frame 1302) land in the right places.

## Known limits
- The floor is soft and patchy: the camera sees the floor at a shallow angle and from 1.3–1.7 m, so the usable texel detail is far below the wall texture, and about half of the floor was never seen clearly.
- Clutter on the floor (debris, tools, the person, tape) is baked into the texture.
- Some patches still show wall or outdoor imagery where the walls or openings are not in the model or in the SfM points (for example near the centre of the plan, where frames looking through windows project outside views onto the floor), and a black-and-white blotch is visible in the lobby between the west flats (it appears to come from the GLB floor textures, but I did not trace it).
- Late-frame texture (128 in the mask) comes from poses that are only good to roughly 15–30 cm.
- The two west-flat GLB floors overlap by about 0.6 m near the mirror line, so that band is double-covered.
- Floor outline is approximate (the wall model is incomplete) and includes a thin fringe, up to about 0.35 m, outside some outer walls.

## Files
`Floor18_textured_Flat1_Flat2_glb_ground.blend`, `floor_ground_texture.png`, `floor_ground_coverage.png`, `floor_stats.json`, `scripts/` (`make_plate.py`, `floor_bake.py`, `build_ground.py`, `export_walls.py`, `render_top.py`, `diag2.py`), `evidence/`.
