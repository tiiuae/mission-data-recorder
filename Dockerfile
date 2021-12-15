FROM ghcr.io/tiiuae/tii-golang-ros:galactic-go1.17 AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN go generate && \
    go build -o mission-data-recorder

FROM ghcr.io/tiiuae/tii-ubuntu-ros:galactic

# All messages available in the repo are installed so that the recorder could
# record as many message types as possible.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    ros-$ROS_DISTRO-rclc \
    ros-$ROS_DISTRO-ackermann-msgs \
    ros-$ROS_DISTRO-action-msgs \
    ros-$ROS_DISTRO-actionlib-msgs \
    ros-$ROS_DISTRO-automotive-autonomy-msgs \
    ros-$ROS_DISTRO-automotive-navigation-msgs \
    ros-$ROS_DISTRO-automotive-platform-msgs \
    ros-$ROS_DISTRO-autoware-auto-msgs \
    ros-$ROS_DISTRO-can-msgs \
    ros-$ROS_DISTRO-cartographer-ros-msgs \
    ros-$ROS_DISTRO-cascade-lifecycle-msgs \
    ros-$ROS_DISTRO-control-msgs \
    ros-$ROS_DISTRO-controller-manager-msgs \
    ros-$ROS_DISTRO-diagnostic-msgs \
    ros-$ROS_DISTRO-dwb-msgs \
    ros-$ROS_DISTRO-fog-msgs \
    ros-$ROS_DISTRO-four-wheel-steering-msgs \
    ros-$ROS_DISTRO-gazebo-msgs \
    ros-$ROS_DISTRO-geographic-msgs \
    ros-$ROS_DISTRO-geometry-msgs \
    ros-$ROS_DISTRO-gps-msgs \
    ros-$ROS_DISTRO-graph-msgs \
    ros-$ROS_DISTRO-grbl-msgs \
    ros-$ROS_DISTRO-lgsvl-msgs \
    ros-$ROS_DISTRO-lifecycle-msgs \
    ros-$ROS_DISTRO-map-msgs \
    ros-$ROS_DISTRO-marti-can-msgs \
    ros-$ROS_DISTRO-marti-common-msgs \
    ros-$ROS_DISTRO-marti-dbw-msgs \
    ros-$ROS_DISTRO-marti-nav-msgs \
    ros-$ROS_DISTRO-marti-perception-msgs \
    ros-$ROS_DISTRO-marti-sensor-msgs \
    ros-$ROS_DISTRO-marti-status-msgs \
    ros-$ROS_DISTRO-marti-visualization-msgs \
    ros-$ROS_DISTRO-mavros-msgs \
    ros-$ROS_DISTRO-micro-ros-diagnostic-msgs \
    ros-$ROS_DISTRO-micro-ros-msgs \
    ros-$ROS_DISTRO-microstrain-inertial-msgs \
    ros-$ROS_DISTRO-moveit-msgs \
    ros-$ROS_DISTRO-nao-command-msgs \
    ros-$ROS_DISTRO-nao-sensor-msgs \
    ros-$ROS_DISTRO-nav-2d-msgs \
    ros-$ROS_DISTRO-nav-msgs \
    ros-$ROS_DISTRO-nav2-msgs \
    ros-$ROS_DISTRO-nmea-msgs \
    ros-$ROS_DISTRO-object-recognition-msgs \
    ros-$ROS_DISTRO-octomap-msgs \
    ros-$ROS_DISTRO-ouster-msgs \
    ros-$ROS_DISTRO-pcl-msgs \
    ros-$ROS_DISTRO-pendulum-msgs \
    ros-$ROS_DISTRO-phidgets-msgs \
    ros-$ROS_DISTRO-plansys2-msgs \
    ros-$ROS_DISTRO-plotjuggler-msgs \
    ros-$ROS_DISTRO-px4-msgs \
    ros-$ROS_DISTRO-radar-msgs \
    ros-$ROS_DISTRO-rc-common-msgs \
    ros-$ROS_DISTRO-rc-reason-msgs \
    ros-$ROS_DISTRO-realsense2-camera-msgs \
    ros-$ROS_DISTRO-rmf-building-map-msgs \
    ros-$ROS_DISTRO-rmf-charger-msgs \
    ros-$ROS_DISTRO-rmf-dispenser-msgs \
    ros-$ROS_DISTRO-rmf-door-msgs \
    ros-$ROS_DISTRO-rmf-fleet-msgs \
    ros-$ROS_DISTRO-rmf-ingestor-msgs \
    ros-$ROS_DISTRO-rmf-lift-msgs \
    ros-$ROS_DISTRO-rmf-task-msgs \
    ros-$ROS_DISTRO-rmf-traffic-msgs \
    ros-$ROS_DISTRO-rmf-visualization-msgs \
    ros-$ROS_DISTRO-rmf-workcell-msgs \
    ros-$ROS_DISTRO-rosapi-msgs \
    ros-$ROS_DISTRO-rosbridge-msgs \
    ros-$ROS_DISTRO-rosbridge-test-msgs \
    ros-$ROS_DISTRO-rosgraph-msgs \
    ros-$ROS_DISTRO-sensor-msgs \
    ros-$ROS_DISTRO-shape-msgs \
    ros-$ROS_DISTRO-smacc2-msgs \
    ros-$ROS_DISTRO-soccer-vision-msgs \
    ros-$ROS_DISTRO-statistics-msgs \
    ros-$ROS_DISTRO-std-msgs \
    ros-$ROS_DISTRO-stereo-msgs \
    ros-$ROS_DISTRO-stubborn-buddies-msgs \
    ros-$ROS_DISTRO-system-modes-msgs \
    ros-$ROS_DISTRO-teleop-tools-msgs \
    ros-$ROS_DISTRO-test-msgs \
    ros-$ROS_DISTRO-tf2-geometry-msgs \
    ros-$ROS_DISTRO-tf2-msgs \
    ros-$ROS_DISTRO-tf2-sensor-msgs \
    ros-$ROS_DISTRO-trajectory-msgs \
    ros-$ROS_DISTRO-turtlebot3-msgs \
    ros-$ROS_DISTRO-ublox-msgs \
    ros-$ROS_DISTRO-ublox-ubx-msgs \
    ros-$ROS_DISTRO-udp-msgs \
    ros-$ROS_DISTRO-unique-identifier-msgs \
    ros-$ROS_DISTRO-urg-node-msgs \
    ros-$ROS_DISTRO-velodyne-msgs \
    ros-$ROS_DISTRO-vision-msgs \
    ros-$ROS_DISTRO-visualization-msgs \
    ros-$ROS_DISTRO-webots-ros2-msgs \
    ros-$ROS_DISTRO-wiimote-msgs && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /build/mission-data-recorder ./
ENTRYPOINT [ "rosexec", "./mission-data-recorder" ]
