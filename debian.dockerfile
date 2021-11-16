# fog-sw BUILDER
FROM ros:foxy-ros-base as fog-sw-builder

ARG BUILD_NUMBER
ARG COMMIT_ID
ARG GIT_VER

# workaround for ROS GPG Key Expiration Incident
RUN rm /etc/apt/sources.list.d/ros2-latest.list && \
    apt-get update && \
    apt-get install -y curl && \
    curl http://repo.ros2.org/repos.key | sudo apt-key add - && \
    echo "deb http://packages.ros.org/ros2/ubuntu focal main" > /etc/apt/sources.list.d/ros2-latest.list && \
    apt-get update

RUN echo "deb [trusted=yes] https://artifactory.ssrc.fi/artifactory/debian-public-local focal fog-sw" >> /etc/apt/sources.list

# Install build dependencies
RUN apt-get update -y && apt-get install -y --no-install-recommends \
    debhelper \
    dh-make \
    fakeroot \
    ros-foxy-ros-core \
    ros-foxy-ros2bag \
    ros-foxy-rosbag2 \
    ros-foxy-rosbag2-compression \
    ros-foxy-rosbag2-converter-default-plugins \
    ros-foxy-rosbag2-cpp \
    ros-foxy-rosbag2-storage \
    ros-foxy-rosbag2-storage-default-plugins \
    ros-foxy-rosbag2-transport \
    && rm -rf /var/lib/apt/lists/*

ENV GO_VERSION=1.17.3
RUN curl "https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz"\
    | tar -C /usr/local -xz
ENV GOPATH=/go
ENV PATH="/usr/local/go/bin:${PATH}:${GOPATH}/bin"

WORKDIR /build

COPY . .

RUN . /opt/ros/foxy/setup.sh && \
    params="-m $(realpath .) " \
    && [ ! "${BUILD_NUMBER}" = "" ] && params="$params -b ${BUILD_NUMBER}" || : \
    && [ ! "${COMMIT_ID}" = "" ] && params="$params -c ${COMMIT_ID}" || : \
    && [ ! "${GIT_VER}" = "" ] && params="$params -g ${GIT_VER}" || : \
    && ./packaging/common/package.sh $params

FROM scratch
COPY --from=fog-sw-builder /build/*.deb /packages/
