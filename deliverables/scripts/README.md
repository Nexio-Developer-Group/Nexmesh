# Run order
Work from one directory that contains `blend/floor.blend`, `video/in.mp4`, `work/`, `frames_all/`. Python 3.13 with `bpy opencv-python-headless numpy scipy trimesh pycolmap embreex rtree`; `libegl1` and `libgl1-mesa-dri` for headless rendering.

1. `ffmpeg -i video/in.mp4 -vf "fps=3,scale=1920:-2" -q:v 2 frames_all/f_%05d.jpg` (add `fps=10` windows as `f_<idx>_<k>.jpg` for the 295–325 s gap)
2. `python3 -I scripts/01_export_model.py blend/floor.blend work/model.npz`
3. `python3 -I scripts/02_blur_filter.py frames_all frames_sharp/img` (informational; all frames were used)
4. `python3 -I scripts/03_colmap.py frames_sharp/img colmap_run1 OPENCV 25`, then `03b_colmap_add_frames.py frames_all colmap_run1 30`, then `03c_map.py colmap_run1/db.db frames_all colmap_run1/sparse`, then `03d_extend.py ... colmap_run1/sparse/0 colmap_run1/ext2`
5. `python3 -I scripts/04_align.py colmap_run1/ext2 work/model.npz work/model.json work/sim3_ext2.json`
6. `python3 -I scripts/07_refine.py colmap_run1/ext2 work/sim3_ext2.json work/poses_refined.json` (writes both refined and unrefined poses; the bake uses the unrefined ones)
7. `python3 -I scripts/05b_verify_poses.py work/poses_refined.json out.jpg unrefined f_00001.jpg,...`
8. `python3 -I scripts/08_bake.py work/poses_refined.json work/model.npz frames_all work/bake 4096 1.0` (use `8192 2.0` with 4K frames)
9. `python3 -I scripts/09_build_blend.py blend/floor.blend work/bake/atlas_uv.npz work/bake/texture_4096.png work/poses_refined.json work/deliver`
10. `python3 -I scripts/10_render_check.py work/deliver/Floor18_textured.blend out_dir 1,120,300`

`06_align_fragments.py` is the blind fragment-alignment attempt. It did not work and is kept only for reference.
