# fog-sw BUILDER
FROM ghcr.io/tiiuae/tii-golang-ros:foxy-go1.17 as fog-sw-builder

ARG BUILD_NUMBER
ARG COMMIT_ID
ARG GIT_VER

# Install build dependencies
RUN apt-get update -y && apt-get install -y --no-install-recommends \
    debhelper \
    dh-make \
    fakeroot

WORKDIR /build

COPY . .

RUN params="-m $(realpath .) " \
    && [ ! "${BUILD_NUMBER}" = "" ] && params="$params -b ${BUILD_NUMBER}" || : \
    && [ ! "${COMMIT_ID}" = "" ] && params="$params -c ${COMMIT_ID}" || : \
    && [ ! "${GIT_VER}" = "" ] && params="$params -g ${GIT_VER}" || : \
    && ./packaging/common/package.sh $params

FROM scratch
COPY --from=fog-sw-builder /build/*.deb /packages/
