"""S6-T5: PyTorch vs ONNX Runtime (CPU) vs TensorRT variants of retriever / XFeat backbone / LighterGlue, each timed alone in ONE process.
Run ALONE (no other load):  python t5_variants.py --run-id S6-T5-20261023-s0
TensorRT: attempted via `pip install tensorrt` (needs NVIDIA toolchain; fails here) -> recorded as not run. No Jetson numbers are measured."""
import argparse, os, sys, json, time, resource, traceback
HERE = os.path.dirname(os.path.abspath(__file__)); ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
sys.path.insert(0, HERE)
import numpy as np, cv2, torch
import s6lib as L
ap = argparse.ArgumentParser(); ap.add_argument("--run-id", default="S6-T5-20261023-s0"); ap.add_argument("--n", type=int, default=10)
ap.add_argument("--tmp", default="/tmp/s6_onnx")
a = ap.parse_args(); os.makedirs(a.tmp, exist_ok=True)
out = os.path.join(ROOT, "results", "s6", "nidar", a.run_id); os.makedirs(out, exist_ok=True)
D = os.path.join(ROOT, "data", "nidar") + "/"
res = {"note": "CPU only, 2 threads; no Jetson, no GPU, TensorRT not available. Jetson numbers NOT measured."}
rss = lambda: resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024
def timeit(f, n):
    f(); ts = []
    for _ in range(n):
        t = time.perf_counter(); f(); ts.append((time.perf_counter() - t) * 1000)
    return dict(p50=float(np.percentile(ts, 50)), p95=float(np.percentile(ts, 95)), rss_mb=rss())
fr = cv2.imread(D + "frames/100.000.png", 0)
import onnxruntime as ort
so = ort.SessionOptions(); so.intra_op_num_threads = 2
# ---- retriever (MegaLoc) 196x196 input
try:
    ret = L.MegaLocRet(); x = torch.rand(1, 3, 196, 196)
    res["megaloc_torch"] = timeit(lambda: ret.m(x), a.n); y0 = ret.m(x).detach().numpy()
    p = a.tmp + "/megaloc.onnx"
    with torch.inference_mode(): torch.onnx.export(ret.m, x, p, input_names=["img"], output_names=["desc"], opset_version=17, dynamo=False)
    s = ort.InferenceSession(p, so, providers=["CPUExecutionProvider"])
    res["megaloc_onnx_cpu"] = timeit(lambda: s.run(None, {"img": x.numpy()}), a.n)
    y1 = s.run(None, {"img": x.numpy()})[0]
    res["megaloc_onnx_cpu"]["cosine_vs_torch"] = float((y0 * y1).sum() / np.linalg.norm(y0) / np.linalg.norm(y1))
    res["megaloc_onnx_cpu"]["onnx_mb"] = sum(os.path.getsize(os.path.join(a.tmp, f)) for f in os.listdir(a.tmp)) / 1e6
except Exception as e:
    res["megaloc_onnx_error"] = traceback.format_exc()[-800:]
for f in os.listdir(a.tmp): os.remove(os.path.join(a.tmp, f))
# ---- XFeat backbone (net) 1024x1024 grayscale
try:
    from modules.xfeat import XFeat
    xf = XFeat(top_k=2048); net = xf.net.eval(); x = torch.rand(1, 1, 1024, 1024)
    res["xfeat_net_torch"] = timeit(lambda: net(x), a.n)
    p = a.tmp + "/xfeat.onnx"
    with torch.inference_mode(): torch.onnx.export(net, x, p, input_names=["img"], output_names=["feats", "kpts", "heat"], opset_version=17, dynamo=False)
    s = ort.InferenceSession(p, so, providers=["CPUExecutionProvider"])
    res["xfeat_net_onnx_cpu"] = timeit(lambda: s.run(None, {"img": x.numpy()}), a.n)
    o0 = net(x)[0].detach().numpy(); o1 = s.run(None, {"img": x.numpy()})[0]
    res["xfeat_net_onnx_cpu"]["max_abs_diff"] = float(np.abs(o0 - o1).max())
except Exception as e:
    res["xfeat_onnx_error"] = traceback.format_exc()[-800:]
# ---- LighterGlue (2048 x 2048 kpts)
try:
    m = L.XFeatLG(2048, True); d0 = m.extract(fr); d1 = m.extract(cv2.resize(fr, None, fx=0.9, fy=0.9))
    res["lighterglue_torch_match"] = timeit(lambda: m.match(d0, d1), a.n)
    res["xfeat_extract_torch_1280x800"] = timeit(lambda: m.extract(fr), a.n)
    try:
        lg = m.x.lighterglue if m.x.lighterglue is not None else None
        raise RuntimeError("LighterGlue (kornia LightGlue) ONNX export: data-dependent pruning/dynamic shapes; attempted via torch.onnx.export -> see below")
    except Exception:
        pass
    from modules.lighterglue import LighterGlue
    lgm = m.x.lighterglue
    class W(torch.nn.Module):
        def __init__(s, n): super().__init__(); s.n = n
        def forward(s, k0, d0, k1, d1):
            sz = torch.tensor([[1024., 1024.]])
            o = s.n({"keypoints0": k0, "descriptors0": d0, "keypoints1": k1, "descriptors1": d1, "image_size0": sz, "image_size1": sz}, min_conf=0.1)
            return o["matches0"], o["matching_scores0"]
    k0 = torch.rand(1, 512, 2) * 1000; d = torch.nn.functional.normalize(torch.rand(1, 512, 64), dim=-1)
    torch.onnx.export(W(lgm), (k0, d, k0, d), a.tmp + "/lg.onnx", opset_version=17, dynamo=False)
    s = ort.InferenceSession(a.tmp + "/lg.onnx", so, providers=["CPUExecutionProvider"])
    res["lighterglue_onnx_cpu_512kpts"] = timeit(lambda: s.run(None, dict(zip([i.name for i in s.get_inputs()], [k0.numpy(), d.numpy(), k0.numpy(), d.numpy()]))), a.n)
except Exception as e:
    res["lighterglue_onnx_error"] = traceback.format_exc()[-800:]
res["tensorrt"] = "not run: `pip install tensorrt` sdist build fails without NVIDIA driver (nvidia-smi not found); tensorrt-cu12 libs wheel install failed (no disk space / no GPU). FP16 engine build requires GPU."
json.dump(res, open(out + "/metrics.json", "w"), indent=1)
open(out + "/log.txt", "w").write(json.dumps(res, indent=1)); print(json.dumps(res, indent=1))
