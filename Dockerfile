FROM ghcr.io/tiiuae/tii-golang-ros:galactic-go1.17 AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download && \
    go install github.com/tiiuae/rclgo/cmd/rclgo-gen
COPY . ./
RUN go generate && \
    go build -o mission-data-recorder

FROM ghcr.io/tiiuae/tii-ubuntu-ros:galactic

RUN apt-get update && \
    apt-get install -y --no-install-recommends ros-$ROS_DISTRO-rclc && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /build/mission-data-recorder ./
ENV APP=/app/mission-data-recorder
ENTRYPOINT [ "bash", "-c", "${APP} $@", "${APP}" ]
