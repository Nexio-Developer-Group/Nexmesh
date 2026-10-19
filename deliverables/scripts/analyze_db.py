import sqlite3,numpy as np,sys
import scipy.sparse as sp, scipy.sparse.csgraph as cg
db=sqlite3.connect(sys.argv[1])
names={i:n for i,n in db.execute('select image_id,name from images')}
def pid2(p): return divmod(p,2147483647)
rows=db.execute('select pair_id,rows from two_view_geometries where rows>0').fetchall()
print("verified pairs",len(rows))
cons={}
for p,n in rows:
    a,b=pid2(p); cons.setdefault(a,{})[b]=n; cons.setdefault(b,{})[a]=n
ids=sorted(names)
link=np.array([cons.get(ids[i],{}).get(ids[i+1],0) for i in range(len(ids)-1)])
print("consecutive inliers: median",np.median(link),"frac<15:",(link<15).mean(),"zero:",(link==0).mean())
for th in (15,30,50):
    r=[];c=[]
    for p,n in rows:
        if n>=th: a,b=pid2(p);r.append(a-1);c.append(b-1)
    G=sp.coo_matrix((np.ones(len(r)),(r,c)),shape=(len(ids),)*2)
    nc,lab=cg.connected_components(G,directed=False)
    print(f"components (edges>={th} inl):",nc,"largest",sorted(np.bincount(lab))[::-1][:8])
