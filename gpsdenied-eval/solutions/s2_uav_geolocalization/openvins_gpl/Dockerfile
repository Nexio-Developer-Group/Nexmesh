# GPLv3 component (OpenVINS). Isolated; do not copy into other solutions.
FROM ros:foxy-ros-base
RUN apt-get update && apt-get install -y --no-install-recommends build-essential cmake git libeigen3-dev libboost-all-dev libceres-dev libopencv-dev ros-foxy-cv-bridge ros-foxy-image-transport ros-foxy-tf2-geometry-msgs ros-foxy-tf2-eigen ros-foxy-rosbag2 ros-foxy-rosbag2-storage-default-plugins python3-colcon-common-extensions python3-numpy python3-opencv && rm -rf /var/lib/apt/lists/*
COPY src /ws/src/open_vins
WORKDIR /ws
RUN . /opt/ros/foxy/setup.sh && nice colcon build --event-handlers console_cohesion+ --cmake-args -DCMAKE_BUILD_TYPE=Release -DENABLE_ROS=ON -DBUILD_OV_EVAL=OFF --parallel-workers 1 --executor sequential 2>&1 | tail -200
