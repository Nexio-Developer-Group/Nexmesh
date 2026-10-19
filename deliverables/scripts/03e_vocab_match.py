"""Loop-closure style matching via vocabulary-tree retrieval. usage: 03e_vocab_match.py db vocab.bin [num_images]"""
import pycolmap, sys, time
db, vocab = sys.argv[1:3]; n = int(sys.argv[3]) if len(sys.argv) > 3 else 40; t = time.time()
po = pycolmap.VocabTreePairingOptions(); po.vocab_tree_path = vocab; po.num_images = n; po.num_images_after_verification = 0; po.max_num_features = 2000
mo = pycolmap.FeatureMatchingOptions(); mo.num_threads = 4
pycolmap.match_vocabtree(db, matching_options=mo, pairing_options=po)
print('vocab matching done', time.time() - t, flush=True)
