"""Add frames to an existing COLMAP db (same camera), re-run sequential matching, map with relaxed thresholds.
usage: 03b_colmap_add_frames.py all_frames_dir workdir overlap"""
import pycolmap, sys, os, time, sqlite3
img, wd, overlap = sys.argv[1], sys.argv[2], int(sys.argv[3]); db = os.path.join(wd, 'db.db')
have = {n for (n,) in sqlite3.connect(db).execute('select name from images')}
new = [f for f in sorted(os.listdir(img)) if f.endswith('.jpg') and f not in have]
t = time.time(); print('new frames', len(new), flush=True)
if new:
    ro = pycolmap.ImageReaderOptions(); ro.existing_camera_id = 1
    fo = pycolmap.FeatureExtractionOptions(); fo.max_image_size = 1920; fo.num_threads = 4
    fo.sift.max_num_features = 8192; fo.sift.peak_threshold = 0.004; fo.sift.edge_threshold = 12
    pycolmap.extract_features(db, img, image_names=new, camera_mode=pycolmap.CameraMode.AUTO, reader_options=ro, extraction_options=fo)
    print('features done', time.time() - t, flush=True)
po = pycolmap.SequentialPairingOptions(); po.overlap = overlap; po.loop_detection = False
mo = pycolmap.FeatureMatchingOptions(); mo.num_threads = 4
pycolmap.match_sequential(db, matching_options=mo, pairing_options=po)
print('matching done', time.time() - t, flush=True)
