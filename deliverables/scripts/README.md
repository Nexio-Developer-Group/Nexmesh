# Run order
Work from one directory that contains `blend/floor.blend`, `video/in.mp4`, `work/`, `frames_all/`. Python 3.13 with `bpy opencv-python-headless numpy scipy trimesh pycolmap embreex rtree torch` and `third_party/LightGlue` (`git clone https://github.com/cvg/LightGlue`); `libegl1` + `libgl1-mesa-dri` for headless rendering. COLMAP vocabulary tree: `https://github.com/colmap/colmap/releases/download/3.11.1/vocab_tree_faiss_flickr100K_words256K.bin` saved as `work/vocab_faiss.bin`.

1. `ffmpeg -i video/in.mp4 -vf "fps=3,scale=1920:-2" -q:v 2 frames_all/f_%05d.jpg`; add 10 fps frames for 295–325 s as `f_<idx>_<k>.jpg`
2. `python3 -I scripts/01_export_model.py blend/floor.blend work/model.npz`
3. `03_colmap.py` (features+matching on `frames_sharp/img`), `03b_colmap_add_frames.py frames_all colmap_run1 30`, `03c_map.py` → model 0, `03d_extend.py` → ext2 (through frame 898)
4. **Loop-closure matching (this is what bridges the blank-wall stretch):** `03e_vocab_match.py colmap_run1/db.db work/vocab_faiss.bin 40`, then `03d_extend.py colmap_run1/db.db frames_all colmap_run1/ext2 colmap_run1/ext3`, then `03d_extend_relaxed.py ... ext3 ext4`
5. `04_align.py colmap_run1/ext2 ...` (anchor Sim3 on the first model), `04b_compose_sim3.py colmap_run1/ext2 colmap_run1/ext4 work/sim3_ext2.json work/sim3_ext4.json`, `04c_export_poses.py colmap_run1/ext4 work/sim3_ext4.json work/poses_sfm_ext4.json`
6. Re-track the late section (after the teleports in the SfM trajectory): `20_seeded_track.py work/poses_sfm_ext4.json work/model.npz frames_all work/poses_sd1.json 1541 work/seeds.json` (seeds.json holds hand-checked poses `{"f_01822.jpg": [-13, -2.2, 1.6, -1, 2]}` = x y z yaw pitch). Helpers for checking by eye: `17_sheet.py` (overlay sheets), `18_plan.py` (top-down path), `19_try.py` (try an absolute pose on a frame). Merge: frames ≤ 1540 from the SfM poses, frames ≥ 1541 from the new file.
7. Verify: `05b_verify_poses.py poses.json out.jpg unrefined name1,name2,...`
8. `08_bake.py work/poses_bake_v3.json work/model.npz frames_all work/bake_v3 4096 1.0` (pose file = corrected poses with `tier`/`wtier` fields added; use `8192 2.0` with 4K frames)
9. `09_build_blend.py blend/floor.blend work/bake_v3/atlas_uv.npz work/bake_v3/texture_4096.png work/poses_bake_v3.json work/deliver3`
10. `10_render_check.py work/deliver3/Floor18_textured.blend out_dir 100,500,...`

Reference / not used in the final result: `02_blur_filter.py` (filter hurt), `06_align_fragments.py` (blind alignment, failed), `07_refine.py` (segment ICP, made things worse), `11_localize_chain.py` and `13_relocalize_bruteforce.py` (model-based localisation experiments), `14_corrected_trajectory.py` and `16_piecewise_track.py` (earlier attempts at the late section, superseded by 20), `15_edge_score.py` (alignment score, not discriminative), `21_edge_refine.py` (automatic edge refinement, rejected).
