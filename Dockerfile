FROM ghcr.io/tiiuae/fog-ros-baseimage:builder-latest AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN rm -rf msgs && \
    go generate && \
    go build -o mission-data-recorder

FROM ghcr.io/tiiuae/fog-ros-baseimage:main

RUN apt-get update && \
    apt-get install -y --no-install-recommends --no-upgrade \
    ros-$ROS_DISTRO-rosbag2 && \
    rm -rf /var/lib/apt/lists/*

# By default all messages available in the repo are installed so that the
# recorder could record as many message types as possible.
ENV MESSAGE_PACKAGES=ros-$ROS_DISTRO-*-msgs

RUN apt-get update && \
    apt-get install -y --no-install-recommends --no-upgrade \
    $MESSAGE_PACKAGES && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /build/mission-data-recorder ./
ENTRYPOINT [ "ros-with-env", "./mission-data-recorder" ]
