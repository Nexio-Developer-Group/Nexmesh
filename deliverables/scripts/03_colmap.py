"""SfM with pycolmap. usage: 03_colmap.py image_dir workdir [model=OPENCV] [overlap=25]"""
import pycolmap, sys, os, time
img, wd = sys.argv[1], sys.argv[2]
model = sys.argv[3] if len(sys.argv) > 3 else 'OPENCV'
overlap = int(sys.argv[4]) if len(sys.argv) > 4 else 25
os.makedirs(wd, exist_ok=True); db = os.path.join(wd, 'db.db')
W, H = 1920, 1080; f = W * 14.68 / 36
params = {'OPENCV': f"{f},{f},{W/2},{H/2},0,0,0,0", 'OPENCV_FISHEYE': f"{f},{f},{W/2},{H/2},0,0,0,0",
          'FULL_OPENCV': f"{f},{f},{W/2},{H/2},0,0,0,0,0,0,0,0"}[model]
t = time.time()
if not os.path.exists(db):
    ro = pycolmap.ImageReaderOptions(); ro.camera_model = model; ro.camera_params = params
    fo = pycolmap.FeatureExtractionOptions(); fo.max_image_size = 1920; fo.num_threads = 4
    fo.sift.max_num_features = 8192; fo.sift.peak_threshold = 0.004; fo.sift.edge_threshold = 12
    pycolmap.extract_features(db, img, camera_mode=pycolmap.CameraMode.SINGLE, reader_options=ro, extraction_options=fo)
    print('features done', time.time() - t, flush=True)
    po = pycolmap.SequentialPairingOptions(); po.overlap = overlap; po.loop_detection = False
    mo = pycolmap.FeatureMatchingOptions(); mo.num_threads = 4
    pycolmap.match_sequential(db, matching_options=mo, pairing_options=po)
    print('matching done', time.time() - t, flush=True)
op = pycolmap.IncrementalPipelineOptions(); op.multiple_models = True; op.min_model_size = 10; op.num_threads = 4
op.ba_refine_principal_point = False; op.mapper.init_min_num_inliers = 50
out = os.path.join(wd, 'sparse'); os.makedirs(out, exist_ok=True)
recs = pycolmap.incremental_mapping(db, img, out, op)
print('mapping done', time.time() - t, flush=True)
for k, r in recs.items(): print(k, r.summary(), flush=True)
