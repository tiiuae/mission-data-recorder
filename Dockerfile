# This image is a temporary solution for distributing mission-data-recorder to
# fog drone images.
FROM golang:1.16 AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN go build -o mission-data-recorder
