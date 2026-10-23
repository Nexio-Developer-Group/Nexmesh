"""S4 recorder (UNTESTED, never executed: the GISNav stack could not be stood up, see SETUP.md).
ROS 2 node that would subscribe to GISNav's output and write harness-format estimates.csv.
Topic names taken from repo/ros/gisnav/gisnav/constants.py (verify against that file before use).
Camera replay side (also not written): publish frames/<t>.png at 5 Hz on the camera image topic
with CameraInfo from data/nidar/camera.json (fx=fy=1108.5, 1280x800), plus MAVROS-style NavSatFix,
PoseStamped (attitude) and a WMS server (MapServer) serving data/nidar/map.tif as layers 'imagery'/'dem'.
"""
import csv, sys
import rclpy
from rclpy.node import Node
from nav_msgs.msg import Odometry   # /robot_localization/odometry/filtered (ECEF/earth frame in GISNav)
import math

def ecef_to_wgs84(x, y, z):
    a = 6378137.0; f = 1/298.257223563; e2 = f*(2-f)
    lon = math.atan2(y, x); p = math.hypot(x, y); lat = math.atan2(z, p*(1-e2))
    for _ in range(5):
        N = a/math.sqrt(1-e2*math.sin(lat)**2); h = p/math.cos(lat)-N
        lat = math.atan2(z, p*(1-e2*N/(N+h)))
    return math.degrees(lat), math.degrees(lon), h

class Rec(Node):
    def __init__(self, out):
        super().__init__("s4_recorder")
        self.f = open(out, "w", newline=""); self.w = csv.writer(self.f)
        self.w.writerow("t,lat,lon,alt,yaw,conf,accepted".split(","))
        self.create_subscription(Odometry, "/robot_localization/odometry/filtered", self.cb, 10)
    def cb(self, m):
        p = m.pose.pose.position; lat, lon, alt = ecef_to_wgs84(p.x, p.y, p.z)
        t = m.header.stamp.sec + m.header.stamp.nanosec*1e-9
        self.w.writerow([t, lat, lon, alt, "", 1.0, 1]); self.f.flush()

if __name__ == "__main__":
    rclpy.init(); rclpy.spin(Rec(sys.argv[1]))
