# Runs INSIDE the ROS2 container: builds a rosbag2 (sqlite3) from NIDAR frames (downsampled 2x) + SITL RAW_IMU.
import sys, os, csv, cv2, numpy as np, rclpy
from rclpy.serialization import serialize_message
from sensor_msgs.msg import Image, Imu
import rosbag2_py
FR, IMU, OUT = sys.argv[1], sys.argv[2], sys.argv[3]
T0 = 1000.0
w = rosbag2_py.SequentialWriter(); w.open(rosbag2_py.StorageOptions(uri=OUT, storage_id='sqlite3'), rosbag2_py.ConverterOptions('', ''))
w.create_topic(rosbag2_py.TopicMetadata(name='/cam0/image_raw', type='sensor_msgs/msg/Image', serialization_format='cdr'))
w.create_topic(rosbag2_py.TopicMetadata(name='/imu0', type='sensor_msgs/msg/Imu', serialization_format='cdr'))
ev = []
for r in csv.DictReader(open(IMU)):
    ev.append((float(r['t']), 'imu', r))
for f in os.listdir(FR):
    if f.endswith('.png'): ev.append((float(f[:-4]), 'img', f))
ev.sort(key=lambda e: e[0])
def stamp(msg, t):
    s = T0 + t; msg.header.stamp.sec = int(s); msg.header.stamp.nanosec = int(round((s - int(s)) * 1e9))
n = 0
for t, kind, x in ev:
    if kind == 'imu':
        m = Imu(); stamp(m, t); m.header.frame_id = 'imu'
        m.linear_acceleration.x, m.linear_acceleration.y, m.linear_acceleration.z = float(x['ax']), float(x['ay']), float(x['az'])
        m.angular_velocity.x, m.angular_velocity.y, m.angular_velocity.z = float(x['gx']), float(x['gy']), float(x['gz'])
        m.orientation_covariance[0] = -1.0
        w.write('/imu0', serialize_message(m), int((T0 + t) * 1e9))
    else:
        g = cv2.imread(os.path.join(FR, x), 0); g = cv2.resize(g, (640, 400), interpolation=cv2.INTER_AREA)
        m = Image(); stamp(m, t); m.header.frame_id = 'cam0'; m.height, m.width, m.encoding, m.step = 400, 640, 'mono8', 640; m.data = g.tobytes()
        w.write('/cam0/image_raw', serialize_message(m), int((T0 + t) * 1e9)); n += 1
print('frames', n, 'events', len(ev))
