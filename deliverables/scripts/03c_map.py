"""Incremental mapping with thresholds relaxed for low-texture, forward-moving drone footage.
usage: 03c_map.py db image_dir out_dir"""
import pycolmap, sys, os, time
db, img, out = sys.argv[1:4]; os.makedirs(out, exist_ok=True); t = time.time()
op = pycolmap.IncrementalPipelineOptions(); op.multiple_models = True; op.min_model_size = 10; op.num_threads = 4
op.ba_refine_principal_point = False
m = op.mapper
m.abs_pose_min_num_inliers = 15; m.abs_pose_min_inlier_ratio = 0.15
m.init_min_num_inliers = 100; m.init_min_tri_angle = 4; m.init_max_forward_motion = 0.95
op.max_num_models = 12
recs = pycolmap.incremental_mapping(db, img, out, op)
print('mapping done', time.time() - t, flush=True)
for k, r in recs.items(): print(k, r.num_reg_images(), r.compute_mean_reprojection_error(), [c.params for c in r.cameras.values()], flush=True)
