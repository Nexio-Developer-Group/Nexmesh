import sys,os,json,cv2,numpy as np
sys.path.insert(0,os.environ['SCRIPTS']);from common import *
P=json.load(open(sys.argv[1]));n=sys.argv[2];Z0=0.0;p=P[n];q=p['params'];R=np.array(p['R_c2w_unrefined']);C=np.array(p['C_unrefined'])
mesh,md=load_model(sys.argv[3]);ren=Renderer(mesh,md['OBJ']);K=np.array([[q[0],0,q[2]],[0,q[1],q[3]],[0,0,1.]])
img=cv2.imread(os.environ['S']+'/frames_all/'+n);und=cv2.undistort(img,K,np.array(q[4:8]),None,K)
dep,tri=ren.render(K,1920,1080,R,C,scale=1/6.);dm=cv2.erode(np.where(np.isfinite(dep),dep,1e4).astype(np.float32),np.ones((3,3),np.uint8))
print("depth map finite fraction",np.isfinite(dep).mean(),"min/median finite depth",np.nanmin(dep[np.isfinite(dep)]),np.median(dep[np.isfinite(dep)]))
ts=0.02;Rm=7.0;W=int(2*Rm/ts);gx=np.arange(W)*ts-Rm+C[0];gy=C[1]+Rm-np.arange(W)*ts;GX,GY=np.meshgrid(gx,gy);X=np.stack([GX,GY,np.full_like(GX,Z0)],-1).reshape(-1,3)
xc=(X-C)@R;z=xc[:,2];u=q[0]*xc[:,0]/np.maximum(z,1e-3)+q[2];v=q[1]*xc[:,1]/np.maximum(z,1e-3)+q[3];ok=(z>0.2)&(u>0)&(u<1920)&(v>0)&(v<1080)
d_at=dm[np.clip((v/6).astype(int),0,dm.shape[0]-1),np.clip((u/6).astype(int),0,dm.shape[1]-1)]
vis=ok&(z<=d_at+0.07+0.03*z);print("projected texels",ok.sum(),"after occlusion test",vis.sum())
def paint(mask):
    out=np.zeros((W*W,3),np.uint8);uu=u[mask].astype(np.float32);vv=v[mask].astype(np.float32);nn=len(uu);pad=(-nn)%1024
    col=cv2.remap(und,np.pad(uu,(0,pad)).reshape(-1,1024),np.pad(vv,(0,pad)).reshape(-1,1024),cv2.INTER_LINEAR).reshape(-1,3)[:nn];out[mask]=col;o=out.reshape(W,W,3).copy();cv2.circle(o,(W//2,W//2),6,(0,0,255),-1);return cv2.resize(o,(560,560))
cv2.imwrite(sys.argv[4],np.hstack([cv2.resize(und,(996,560)),paint(ok),paint(vis)]))
