"""Gyro-integrated roll/pitch (our own simple AHRS, from data/nidar/imu.csv).  Yaw is taken from the compass hint.
Starts level (the vehicle is on/near hover at the start of the log); accelerometer is not used for tilt because in
steady cruise the specific force is along the thrust axis and carries no tilt information."""
import numpy as np, pandas as pd

def integrate_attitude(imu_csv, yaw0=0.0, acc_tau=None):
    d = pd.read_csv(imu_csv)
    t = d.t.values; g = d[['gx', 'gy', 'gz']].values
    q = np.array([np.cos(yaw0 / 2), 0, 0, np.sin(yaw0 / 2)])   # w,x,y,z ; body->NED
    out = np.zeros((len(t), 3)); R = quat_to_R(q)
    for i in range(len(t)):
        if i:
            dt = t[i] - t[i - 1]; w = g[i] * dt; th = np.linalg.norm(w)
            if th > 1e-12:
                ax = w / th; dq = np.concatenate([[np.cos(th / 2)], ax * np.sin(th / 2)])
            else:
                dq = np.array([1, 0, 0, 0.0])
            q = qmul(q, dq); q /= np.linalg.norm(q)
        R = quat_to_R(q)
        out[i] = [np.arctan2(R[2, 1], R[2, 2]), -np.arcsin(np.clip(R[2, 0], -1, 1)), np.arctan2(R[1, 0], R[0, 0])]
    return t, out   # roll, pitch, yaw (rad); same convention as render_video R = Rz Ry Rx

def qmul(a, b):
    w1, x1, y1, z1 = a; w2, x2, y2, z2 = b
    return np.array([w1*w2-x1*x2-y1*y2-z1*z2, w1*x2+x1*w2+y1*z2-z1*y2, w1*y2-x1*z2+y1*w2+z1*x2, w1*z2+x1*y2-y1*x2+z1*w2])

def quat_to_R(q):
    w, x, y, z = q
    return np.array([[1-2*(y*y+z*z), 2*(x*y-z*w), 2*(x*z+y*w)],
                     [2*(x*y+z*w), 1-2*(x*x+z*z), 2*(y*z-x*w)],
                     [2*(x*z-y*w), 2*(y*z+x*w), 1-2*(x*x+y*y)]])


def complementary_tilt(imu_csv, k=0.02):
    """Complementary filter: gyro Euler-rate integration for roll/pitch, pulled towards the accelerometer tilt
    (SITL accel reads -g rotated by attitude, so tilt is observable) with gain k per 50 Hz sample (tau ~ 1 s)."""
    d = pd.read_csv(imu_csv); t = d.t.values
    ax, ay, az = d.ax.values, d.ay.values, d.az.values; p_, q_, r_ = d.gx.values, d.gy.values, d.gz.values
    roll = np.arctan2(-ay[0], -az[0]); pitch = np.arctan2(ax[0], np.hypot(ay[0], az[0]))
    out = np.zeros((len(t), 2))
    for i in range(len(t)):
        if i:
            dt = t[i] - t[i - 1]
            rd = p_[i] + (q_[i] * np.sin(roll) + r_[i] * np.cos(roll)) * np.tan(pitch)
            pd_ = q_[i] * np.cos(roll) - r_[i] * np.sin(roll)
            roll += rd * dt; pitch += pd_ * dt
        ra = np.arctan2(-ay[i], -az[i]); pa = np.arctan2(ax[i], np.hypot(ay[i], az[i]))
        roll += k * (ra - roll); pitch += k * (pa - pitch)
        out[i] = roll, pitch
    return t, out
