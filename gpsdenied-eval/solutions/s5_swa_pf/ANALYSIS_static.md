## 1. How it works (written from the code that was run)

Plain language. The filter keeps a cloud of guesses ("particles"), each a guess of the drone's map position (x, y), altitude and heading. Every frame:
1. The drone image is turned into a coarse "what is this" picture by a small neural net (SegFormer-B0, 7 classes, repo weights `logs/8_b0`). Only four of its classes matter downstream: ground/other, building (roof and wall are merged: `pr==5 -> 1` in SWAPF.py), road, vegetation. Vehicle (4) and water (6) are produced but ignored by the weights.
2. The map is a satellite image that was labelled once, offline, with the same 4 classes (`label.png`). For every class a distance map is precomputed: for each map pixel, the distance (clipped at 100 px) to the nearest pixel of that class.
3. Each particle cuts out the piece of that class-distance map the drone would see from its guessed altitude (`cut = alt * cam_view * height_to_pix / 2` pixels each side), rescales it to 400x400 (so altitude changes only the cut size, not the comparison size), and compares it with the drone class image, which was pre-rotated in 100 steps of 3.6 deg (25 rotations x 4 quarter turns) and rescaled to 400x400. The score is the sum over pixels of "drone says class c here" x "distance to nearest class c in the map crop", multiplied by a weight that grows with distance from the crop centre (`wan`), plus a huge penalty if the crop sticks out of the map. Low sum = good match. Particle weight = 400*400 / (sum^2).
4. Particles are resampled by weight (80 % of them survive each frame, floor 1000), then moved with the odometry (distance and bearing of motion, heading change) plus Gaussian noise in x, y, altitude and yaw.
5. Position estimate: when the cloud's spread is below a tenth of its initial spread, the particles are clustered (DBSCAN) and the cluster centre is taken (`center()` in PFtool.py; it also prints the error against ground truth, which is only a diagnostic).

Initialisation (PFtool, hard-coded `init_method_mode`): 0 full random over map x, y, altitude and yaw; 1 layered (4 altitude floors, same xy/yaw); 2 xy sampled on building pixels ("semantic alignment").

### What the "odometry" in SWAPF.py really is (leakage)
`SWAPF.py` reads `gps.txt` (ground-truth latitude, longitude, height, heading of every frame) and calls `particles.move(lat - old_lat, lon - old_lon, height - old_height, theta, old_theta)`. So the motion model receives the exact, noise-free ground-truth displacement, altitude change and heading of the drone, and the particle noise (gauss sigma 15 px in x,y, 8 m in z, 0.2 deg in yaw) is the only uncertainty. Initial particles are random (no pose leak), but the heading is known exactly after the first frame (all particles inherit the true heading change, the true heading offset is only found by the 100-way yaw search). `init_particles_goal`/`update_goal_1` put the ground truth in a "goal" particle used only for drawing. Hence the paper's 6.57 m is a filter with perfect relative motion and heading; there is no drift to fight. `center()` is commented out in the demo, so the demo as shipped saves overlay pictures only and outputs no estimate. Our replay replaces this with noisy telemetry odometry (see below).

## 2. Architecture (data-flow stages)
1. Frame (BGR 1280x800) -> optional extension: ortho-rectification with IMU roll/pitch (used only for the oblique stress set).
2. SegFormer-B0 (PyTorch 1.10 CPU, 512x512 letterbox, 7 classes) -> per-pixel class image; roof merged into building.
3. 100 pre-rotated 400x400 4-channel binary class images (torchvision rotate x25 + rot90 x3), multiplied by the bowl weight; integral images (adapter speed-up; repo does the same sum per particle).
4. Offline: satellite map -> label.png (paper: U-Net-VGG, 4 classes; ours: same SegFormer) -> per-class clipped distance transforms (cv2.distanceTransform) + padded copies.
5. Particle array (N x 6: x, y, altitude, yaw, weight, cumulative weight); weight = 160000/sum^2; resampling by searchsorted (80 % kept, min 1000).
6. Motion update with odometry (distance, bearing, yaw change) + Gaussian noise.
7. Spread check + DBSCAN (scikit-learn) -> cluster centre (x, y, altitude, circular-mean yaw) -> lat/lon through map georeference; accepted only when converged and the main cluster holds >= 50 % of particles.
