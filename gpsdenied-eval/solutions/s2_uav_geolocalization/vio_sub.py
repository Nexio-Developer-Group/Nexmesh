"""OUR-VIO-SUBSTITUTE, NOT OpenVINS.

A simple camera+IMU odometry used only because OpenVINS could not be run (see SETUP.md).  Per frame:
  1. attitude: roll/pitch from our complementary filter on imu.csv (gyro + accel tilt), yaw from the compass hint;
  2. barometric height (hint) gives the ground scale;
  3. the frame is resampled onto a north-up ground plane (same geometry as the author's make_queries_oriented.py);
  4. cv2.phaseCorrelate (global optical flow) between consecutive ground-plane images gives metric ground shift;
  5. vehicle displacement = ground shift minus change of the principal-point offset (attitude changes).
When the correlation response is low (blur/haze) the last velocity is propagated (dead reckoning).
Output: east/north metres relative to the start (map frame, north aligned because yaw comes from the compass).
It is NOT visual-inertial in the MSCKF sense, estimates no scale/bias and uses noise-free truth yaw and altitude hints, so its
drift is optimistic compared with a real VIO."""
import numpy as np, cv2
from s2lib import query_crop_att

class VioSubstitute:
    def __init__(self, crop_m=90.0, out_px=180, min_resp=0.05, yaw_bias_deg=0.0, scale_err=0.0):
        self.crop_m, self.out, self.min_resp = crop_m, out_px, min_resp
        self.mpp = crop_m / out_px
        self.yaw_bias = np.radians(yaw_bias_deg); self.scale_err = scale_err   # optional degradation (sensitivity test only)
        self.prev = None; self.prev_off = None; self.prev_t = None
        self.pos = np.zeros(2)       # east, north (m)
        self.vel = np.zeros(2)
        self.win = cv2.createHanningWindow((out_px, out_px), cv2.CV_32F)
        self.n_lost = 0; self.last_resp = 1.0

    def step(self, frame_gray, t, roll, pitch, yaw, alt):
        yaw = yaw + self.yaw_bias
        crop, (n0, e0) = query_crop_att(frame_gray, roll, pitch, yaw, alt, self.crop_m, out=self.out)
        img = cv2.GaussianBlur(crop, (0, 0), 1.0).astype(np.float32)
        img = img - cv2.GaussianBlur(img, (0, 0), 12)           # remove illumination / haze gradient
        off = np.array([e0, n0])
        if self.prev is None:
            self.prev, self.prev_off, self.prev_t = img, off, t
            return self.pos.copy(), 1.0
        dt = max(t - self.prev_t, 1e-3)
        (sx, sy), resp = cv2.phaseCorrelate(self.prev, img, self.win)
        # content moved by (sx, sy) px (x east, y south):  c_k - c_{k-1} = -shift*mpp ;  dP = dc - dOff
        dc = np.array([-sx * self.mpp, +sy * self.mpp])         # east, north
        d = dc - (off - self.prev_off)
        ok = resp >= self.min_resp and np.linalg.norm(d) < 2.5 * max(np.linalg.norm(self.vel) * dt, 1.0) + 2.0
        if ok:
            d = d * (1.0 + self.scale_err)
            self.vel = d / dt
        else:
            d = self.vel * dt; self.n_lost += 1
        self.pos += d
        self.prev, self.prev_off, self.prev_t = img, off, t
        self.last_resp = float(resp)
        return self.pos.copy(), float(resp)
