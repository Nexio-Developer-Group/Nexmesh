"""Extend an existing model by registering remaining images. usage: 03d_extend.py db img in_model out_dir"""
import pycolmap, sys, os, time
db, img, inp, out = sys.argv[1:5]; os.makedirs(out, exist_ok=True); t = time.time()
op = pycolmap.IncrementalPipelineOptions(); op.multiple_models = False; op.num_threads = 4
op.ba_refine_principal_point = False; op.ba_refine_focal_length = False; op.ba_refine_extra_params = False
m = op.mapper; m.abs_pose_min_num_inliers = 15; m.abs_pose_min_inlier_ratio = 0.15
recs = pycolmap.incremental_mapping(db, img, out, op, input_path=inp)
for k, r in recs.items(): print(k, r.num_reg_images(), r.compute_mean_reprojection_error(), time.time() - t, flush=True)
