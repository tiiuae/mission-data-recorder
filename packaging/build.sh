#!/bin/bash

build_dir=$1
dest_dir=$2

cp ${build_dir}/packaging/debian/* ${dest_dir}/DEBIAN/

cd ${build_dir}
go mod download
go generate
go build -o mission-data-recorder || exit
mkdir -p ${dest_dir}/usr/bin
cp -f mission-data-recorder ${dest_dir}/usr/bin/ && go clean || exit
