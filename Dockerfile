# fog-sw BUILDER
FROM ros:galactic-ros-base as fog-sw-builder

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
    golang-1.16 \
    debhelper \
    dh-make \
    fakeroot \
    ros-galactic-ros-core \
    ros-galactic-ros2bag \
    ros-galactic-rosbag2 \
    ros-galactic-rosbag2-compression \
    ros-galactic-rosbag2-converter-default-plugins \
    ros-galactic-rosbag2-cpp \
    ros-galactic-rosbag2-storage \
    ros-galactic-rosbag2-storage-default-plugins \
    ros-galactic-rosbag2-transport \
    && rm -rf /var/lib/apt/lists/*

ENV PATH="/usr/lib/go-1.16/bin/:${PATH}"

WORKDIR /build

COPY . .

RUN params="-m $(realpath .) " \
    && [ ! "${BUILD_NUMBER}" = "" ] && params="$params -b ${BUILD_NUMBER}" || : \
    && [ ! "${COMMIT_ID}" = "" ] && params="$params -c ${COMMIT_ID}" || : \
    && [ ! "${GIT_VER}" = "" ] && params="$params -g ${GIT_VER}" || : \
    && ./packaging/common/package.sh $params

FROM scratch
COPY --from=fog-sw-builder /build/*.deb /packages/
