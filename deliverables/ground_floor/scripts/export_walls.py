import bpy, sys, numpy as np, json
bpy.ops.wm.open_mainfile(filepath=sys.argv[1])
V=[];F=[];OBJ=[];names=[];off=0
for i,o in enumerate(o for o in bpy.data.objects if o.type=='MESH' and not o.name.startswith(('Flat2_','Flat1m_'))):
    me=o.data; me.calc_loop_triangles(); M=np.array(o.matrix_world)
    v=np.array([tuple(x.co) for x in me.vertices]).reshape(-1,3); v=(M[:3,:3]@v.T).T+M[:3,3]
    for t in me.loop_triangles: F.append([off+k for k in t.vertices]); OBJ.append(i)
    V.append(v); off+=len(v); names.append(o.name)
np.savez(sys.argv[2],V=np.vstack(V),F=np.array(F),OBJ=np.array(OBJ)); json.dump({'names':names},open(sys.argv[2].replace('.npz','.json'),'w')); print("walls",len(names),"tris",len(F))
