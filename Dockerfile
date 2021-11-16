FROM ghcr.io/tiiuae/tii-golang-ros:latest AS builder

SHELL [ "/bin/bash", "-c" ]

ENV GOPATH=/go
ENV PATH="${PATH}:${GOPATH}/bin"

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download && \
    go install github.com/tiiuae/rclgo/cmd/rclgo-gen
COPY . ./
RUN . /opt/ros/foxy/setup.sh && \
    go generate && \
    go build -o mission-data-recorder

FROM ghcr.io/tiiuae/tii-ubuntu-ros:latest

RUN apt-get update && \
    apt-get install -y --no-install-recommends ros-foxy-rclc && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /build/run.sh /build/mission-data-recorder ./
ENTRYPOINT [ "./run.sh", "./mission-data-recorder" ]
