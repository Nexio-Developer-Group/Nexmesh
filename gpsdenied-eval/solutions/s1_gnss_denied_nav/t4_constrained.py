"""S1-T4 constrained: ONNX export attempt of the retriever (DINOv2) and matcher (SuperPoint, LightGlue),
and single-process CPU timing of each variant (PyTorch fp32 vs ONNX Runtime). Writes results/s1/nidar/<run-id>/t4.json.

Lead command (alone, optionally under taskset/cgroup limits for the constrained profile):
  OMP_NUM_THREADS=2 taskset -c 0,1 solutions/s1_gnss_denied_nav/env/bin/python solutions/s1_gnss_denied_nav/t4_constrained.py --id S1-T4 --threads 2
Models are exported to solutions/s1_gnss_denied_nav/cache/onnx (git-ignored).
"""
import argparse, json, os, sys, time, traceback, resource
from pathlib import Path
import numpy as np

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
sys.path.insert(0, str(HERE / "repo")); sys.path.insert(0, str(HERE))
os.environ.setdefault("HF_HOME", str(HERE / "hf_cache"))
ap = argparse.ArgumentParser(); ap.add_argument("--id", default="S1-T4"); ap.add_argument("--threads", type=int, default=2)
ap.add_argument("--reps", type=int, default=10); ap.add_argument("--date", default=time.strftime("%Y%m%d"))
a = ap.parse_args()
os.environ["OMP_NUM_THREADS"] = str(a.threads)
import torch
torch.set_num_threads(a.threads)
out = ROOT / f"results/s1/nidar/{a.id}-{a.date}-s0"; out.mkdir(parents=True, exist_ok=True)
onx = HERE / "cache" / "onnx"; onx.mkdir(parents=True, exist_ok=True)
res = {"threads": a.threads, "torch": torch.__version__}


def timeit(fn, reps):
    fn(); ts = []
    for _ in range(reps):
        t = time.perf_counter(); fn(); ts.append((time.perf_counter() - t) * 1000)
    return {"p50_ms": float(np.percentile(ts, 50)), "p95_ms": float(np.percentile(ts, 95))}


def attempt(name, f):
    try:
        res[name] = f()
    except Exception as e:  # record exactly why
        res[name] = {"status": "FAILED", "error": f"{type(e).__name__}: {str(e)[:600]}", "trace_tail": traceback.format_exc()[-800:]}


import onnxruntime as ort
so = ort.SessionOptions(); so.intra_op_num_threads = a.threads


def retriever():
    from gdnav.embed import DinoEmbedder
    m = DinoEmbedder("facebook/dinov2-base", "cls+gem").eval()
    x = torch.rand(1, 3, 224, 224)
    r = {"torch_fp32": timeit(lambda: m(x), a.reps)}
    p = onx / "dinov2_base_cls_gem.onnx"
    with torch.no_grad():
        torch.onnx.export(m, (x,), str(p), input_names=["x"], output_names=["emb"], opset_version=17, dynamo=False)
    s = ort.InferenceSession(str(p), so, providers=["CPUExecutionProvider"])
    xn = x.numpy()
    y = s.run(None, {"x": xn})[0]; r["max_abs_diff_vs_torch"] = float(np.abs(y - m(x).detach().numpy()).max())
    r["onnx_fp32"] = timeit(lambda: s.run(None, {"x": xn}), a.reps); r["onnx_size_mb"] = p.stat().st_size / 1e6
    from onnxruntime.quantization import quantize_dynamic, QuantType
    q = onx / "dinov2_base_cls_gem_int8.onnx"
    quantize_dynamic(str(p), str(q), weight_type=QuantType.QInt8)
    sq = ort.InferenceSession(str(q), so, providers=["CPUExecutionProvider"])
    yq = sq.run(None, {"x": xn})[0]
    r["onnx_int8_dynamic"] = timeit(lambda: sq.run(None, {"x": xn}), a.reps); r["int8_size_mb"] = q.stat().st_size / 1e6
    r["int8_cosine_vs_fp32"] = float((yq * y).sum() / (np.linalg.norm(yq) * np.linalg.norm(y))); r["status"] = "OK"
    return r


def superpoint():
    from lightglue import SuperPoint
    sp = SuperPoint(max_num_keypoints=2048).eval()
    x = torch.rand(1, 1, 800, 1280)
    r = {"torch_fp32_800x1280": timeit(lambda: sp.extract(x.repeat(1, 3, 1, 1)), max(3, a.reps // 2))}
    # export only the dense CNN part (keypoint NMS/top-k + descriptor sampling are data-dependent python ops)
    class Dense(torch.nn.Module):
        def __init__(s, sp): super().__init__(); s.sp = sp
        def forward(s, im):
            x = s.sp.relu(s.sp.conv1a(im)); x = s.sp.relu(s.sp.conv1b(x)); x = s.sp.pool(x)
            x = s.sp.relu(s.sp.conv2a(x)); x = s.sp.relu(s.sp.conv2b(x)); x = s.sp.pool(x)
            x = s.sp.relu(s.sp.conv3a(x)); x = s.sp.relu(s.sp.conv3b(x)); x = s.sp.pool(x)
            x = s.sp.relu(s.sp.conv4a(x)); x = s.sp.relu(s.sp.conv4b(x))
            c = s.sp.relu(s.sp.convPa(x)); sc = s.sp.convPb(c)
            d = s.sp.convDb(s.sp.relu(s.sp.convDa(x)))
            return sc, d
    d = Dense(sp).eval(); im = torch.rand(1, 1, 800, 1280)
    p = onx / "superpoint_dense.onnx"
    with torch.no_grad():
        torch.onnx.export(d, (im,), str(p), input_names=["im"], output_names=["scores", "desc"], opset_version=17, dynamo=False)
    s = ort.InferenceSession(str(p), so, providers=["CPUExecutionProvider"]); inn = im.numpy()
    r["dense_cnn_torch"] = timeit(lambda: d(im), max(3, a.reps // 2)); r["dense_cnn_onnx"] = timeit(lambda: s.run(None, {"im": inn}), max(3, a.reps // 2))
    r["note"] = "only dense CNN exported; NMS/top-k/descriptor sampling stay in python"; r["status"] = "PARTIAL"
    return r


def lightglue():
    from lightglue import LightGlue
    lg = LightGlue(features="superpoint", flash=False).eval()
    n0, n1 = 512, 1024
    data = {"image0": {"keypoints": torch.rand(1, n0, 2) * 280, "descriptors": torch.randn(1, n0, 256), "image_size": torch.tensor([[280., 280.]])},
            "image1": {"keypoints": torch.rand(1, n1, 2) * 900, "descriptors": torch.randn(1, n1, 256), "image_size": torch.tensor([[900., 900.]])}}
    r = {"torch_fp32": timeit(lambda: lg(data), max(3, a.reps // 2))}

    class Wrap(torch.nn.Module):
        def __init__(s, lg): super().__init__(); s.lg = lg
        def forward(s, k0, d0, k1, d1):
            o = s.lg({"image0": {"keypoints": k0, "descriptors": d0, "image_size": torch.tensor([[280., 280.]])},
                      "image1": {"keypoints": k1, "descriptors": d1, "image_size": torch.tensor([[900., 900.]])}})
            return o["matches0"], o["matching_scores0"]
    w = Wrap(lg).eval()
    args = (data["image0"]["keypoints"], data["image0"]["descriptors"], data["image1"]["keypoints"], data["image1"]["descriptors"])
    p = onx / "lightglue.onnx"
    lg.conf.depth_confidence = -1; lg.conf.width_confidence = -1   # disable adaptive early-exit (data-dependent control flow)
    with torch.no_grad():
        torch.onnx.export(w, args, str(p), input_names=["k0", "d0", "k1", "d1"], output_names=["m0", "s0"], opset_version=17, dynamo=False)
    s = ort.InferenceSession(str(p), so, providers=["CPUExecutionProvider"]); feed = {k: v.numpy() for k, v in zip(["k0", "d0", "k1", "d1"], args)}
    r["onnx_fp32_fixed_depth_no_early_exit"] = timeit(lambda: s.run(None, feed), max(3, a.reps // 2)); r["onnx_size_mb"] = p.stat().st_size / 1e6
    r["status"] = "OK (adaptive depth/width pruning disabled for export)"
    return r


for n, f in [("retriever_dinov2_base", retriever), ("matcher_superpoint", superpoint), ("matcher_lightglue", lightglue)]:
    attempt(n, f)
    print(n, json.dumps(res[n])[:500], flush=True)
res["peak_rss_mb"] = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024
json.dump(res, open(out / "t4.json", "w"), indent=1)
print("wrote", out / "t4.json")
