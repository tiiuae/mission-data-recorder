# mission-data-recorder

mission-data-recorder records ROS2 messages from specified topics, stores them in rosbag files and sends them to cloud storage.

## Building

First make sure that you have sourced the ROS 2 environment.

    source /opt/ros/galactic/setup.sh

Go message bindings for ROS 2 interfaces must be generated once before building
and every time the interface definitions change.

    go generate ./...

The application can be build by running

    go build -o mission-data-recorder

## Running

Usage information can be displayed by running

    ./mission-data-recorder --help
