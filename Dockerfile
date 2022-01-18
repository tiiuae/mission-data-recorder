ARG ROS_DISTRO=galactic

# By default all messages available in the repo are installed so that the
# recorder could record as many message types as possible.
ARG MESSAGE_PACKAGES=ros-$ROS_DISTRO-*-msgs

FROM ghcr.io/tiiuae/tii-golang-ros:$ROS_DISTRO-go1.17 AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN go generate && \
    go build -o mission-data-recorder

FROM ghcr.io/tiiuae/tii-ubuntu-ros:$ROS_DISTRO

ARG MESSAGE_PACKAGES

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    $MESSAGE_PACKAGES && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /build/mission-data-recorder ./
ENTRYPOINT [ "rosexec", "./mission-data-recorder" ]
