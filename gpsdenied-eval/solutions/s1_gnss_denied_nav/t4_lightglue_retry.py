"""LightGlue ONNX export attempts 2 and 3 (attempt 1 is in t4_constrained.py)."""
import torch, json, traceback, sys
from lightglue import LightGlue
lg = LightGlue(features="superpoint", flash=False).eval(); lg.conf.depth_confidence=-1; lg.conf.width_confidence=-1
k0,d0,k1,d1 = torch.rand(1,512,2)*280, torch.randn(1,512,256), torch.rand(1,1024,2)*900, torch.randn(1,1024,256)
class W(torch.nn.Module):
    def __init__(s): super().__init__(); s.lg=lg
    def forward(s,k0,d0,k1,d1):
        o=s.lg({"image0":{"keypoints":k0,"descriptors":d0,"image_size":torch.tensor([[280.,280.]])},"image1":{"keypoints":k1,"descriptors":d1,"image_size":torch.tensor([[900.,900.]])}}); return o["matches0"],o["matching_scores0"]
res={}
for name,kw in [("attempt2_dynamo_opset18",dict(dynamo=True,opset_version=18)),("attempt3_torchscript_opset16",dict(dynamo=False,opset_version=16))]:
    try:
        r=torch.onnx.export(W().eval(),(k0,d0,k1,d1),**kw); 
        if r is not None: r.save("/tmp/lg_try.onnx")
        res[name]="OK"
    except Exception as e: res[name]=f"{type(e).__name__}: {str(e)[:500]}"
    print(name,res[name],flush=True)
json.dump(res,open(sys.argv[1],"w"),indent=1)
# timing of the dynamo-exported model (ORT, 2 threads) vs torch
import onnxruntime as ort, numpy as np, time
r=torch.onnx.export(W().eval(),(k0,d0,k1,d1),dynamo=True,opset_version=18); r.save("/tmp/lg_try.onnx")
so=ort.SessionOptions(); so.intra_op_num_threads=2
s=ort.InferenceSession("/tmp/lg_try.onnx",so,providers=["CPUExecutionProvider"]); f=dict(zip(["k0","d0","k1","d1"],[x.numpy() for x in (k0,d0,k1,d1)]))
names=[i.name for i in s.get_inputs()]; f=dict(zip(names,[x.numpy() for x in (k0,d0,k1,d1)]))
def T(fn,n=4):
    fn(); t=time.perf_counter()
    for _ in range(n): fn()
    return (time.perf_counter()-t)/n*1000
torch.set_num_threads(2)
with torch.no_grad(): m=W().eval(); tt=T(lambda:m(k0,d0,k1,d1))
res["torch_fixed_depth_ms"]=tt; res["onnx_dynamo_ms"]=T(lambda:s.run(None,f))
import os; res["onnx_size_mb"]=os.path.getsize("/tmp/lg_try.onnx")/1e6
print(res); json.dump(res,open(sys.argv[1],"w"),indent=1)
