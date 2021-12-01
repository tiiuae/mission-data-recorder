#!/bin/bash

set -e

usage() {
	echo "
Usage: $(basename "$0") [-h] [-m dir] [-b nbr] [-d dist] [-a arch] [-c commit_id] [-v version]
 -- Generate debian package from fog_sw module.
Params:
    -h  Show help text.
    -m  Module directory to generate deb package from (MANDATORY).
    -b  Build number. This will be tha last digit of version string (x.x.N).
    -d  Distribution string in debian changelog.
    -a  Target architecture the module is built for. e.g. amd64, arm64.
    -r  ROS2 node packaging.
    -c  Commit id of the git repository HEAD
    -s  Subdirectory to be packaging
    -g  Git version string
	-v  Package version
"
	exit 0
}

check_arg() {
	if [ "$(echo $1 | cut -c1)" = "-" ]; then
		return 1
	else
		return 0
	fi
}

error_arg() {
	echo "$0: option requires an argument -- $1"
	usage
}

mod_dir=""
build_nbr=0
distr=""
arch=""
version=""
ros=0
git_commit_hash=""
git_version_string=""
packaging_subdir=""
package_version=""

while getopts "hm:b:d:a:rc:g:s:v:" opt
do
	case $opt in
		h)
			usage
			;;
		m)
			check_arg $OPTARG && mod_dir=$OPTARG || error_arg $opt
			;;
		b)
			check_arg $OPTARG && build_nbr=$OPTARG || error_arg $opt
			;;
		d)
			check_arg $OPTARG && distr=$OPTARG || error_arg $opt
			;;
		a)
			check_arg $OPTARG && arch=$OPTARG || error_arg $opt
			;;
		r)
			ros=1
			;;
		c)
			check_arg $OPTARG && git_commit_hash=$OPTARG || error_arg $opt
			;;
		g)
			check_arg $OPTARG && git_version_string=$OPTARG || error_arg $opt
			;;
		s)
			check_arg $OPTARG && packaging_subdir=$OPTARG || error_arg $opt
			;;
		v)
			check_arg $OPTARG && package_version=$OPTARG || error_arg $opt
			;;
		\?)
			usage
			;;
	esac
done

if [ "$mod_dir" = "" ]; then
	echo "$0: Module directory is mandatory option!"
	usage
else
	## Remove trailing '/' mark in module dir, if exists
	mod_dir=$(echo $mod_dir | sed 's/\/$//')
fi

if [ $ros = 1 ]; then
	:
else
	[ "$distr" = "" ] && distr="focal"
	[ "$arch" = "" ] && arch="amd64"
	version="1.0.0"
fi


## Debug prints
echo 
echo "mod_dir: $mod_dir"
echo "build_nbr: $build_nbr"
echo "distr: $distr"
echo "arch: $arch"
echo "ros: $ros"
echo "git_commit_hash: $git_commit_hash"
echo "git_version_string: $git_version_string"
echo "packaging_subdir: $packaging_subdir"
echo "package_version: $package_version"

script_dir=$(realpath $(dirname "$0"))

if [ "$packaging_subdir" != "" ]; then
	cd $mod_dir/$packaging_subdir
else
	cd $mod_dir
fi

if [ "$package_version" == "" ]; then
	package_version=1.0.0
fi

## Generate package
echo "Creating deb package..."
if [ $ros = 1 ]; then
### ROS2 Packaging

	### Create version string
	version=$(grep "<version>" package.xml | sed 's/[^>]*>\([^<"]*\).*/\1/')

	echo "version: ${version}"

	$script_dir/rosdep.sh $mod_dir

	title="$version ($(date +%Y-%m-%d))"
	cat << EOF_CHANGELOG > CHANGELOG.rst
$title
$(printf '%*s' "${#title}" | tr ' ' "-")
* commit: ${git_commit_hash}
EOF_CHANGELOG

	bloom-generate rosdebian --os-name ubuntu --os-version focal --ros-distro galactic --place-template-files \
	&& sed -i "s/@(DebianInc)@(Distribution)/@(DebianInc)/" debian/changelog.em \
	&& [ ! "$distr" = "" ] && sed -i "s/@(Distribution)/${distr}/" debian/changelog.em || : \
	&& bloom-generate rosdebian --os-name ubuntu --os-version focal --ros-distro galactic --process-template-files -i ${build_nbr}${git_version_string} \
	&& sed -i 's/^\tdh_shlibdeps.*/& --dpkg-shlibdeps-params=--ignore-missing-info/g' debian/rules \
	&& fakeroot debian/rules clean \
	&& fakeroot debian/rules binary || exit 1

	# if building sub_path then move package to root
	if ! [ ./ -ef $mod_dir ]; then
		mv ../*.deb $mod_dir/../
	fi

else

	build_dir=$(mktemp -d)
	mkdir ${build_dir}/DEBIAN

	## Build the module
	##   module build.sh contains actions:
	##    - building binaries from sources
	##    - copy artifacts to the build_dir
	echo "Build the module..."
	if [ -e ./packaging/build.sh ]; then
		./packaging/build.sh $PWD ${build_dir} || exit 1
	else
		echo "ERROR: No build script available"
		exit 1
	fi

	if [ -e ./packaging/package.sh ]; then
		echo "INFO: Use package script provided by module."
		./packaging/package.sh $PWD || exit 1
	else
		echo "INFO: Use default packaging."
		### Create version string
		version="$package_version-${build_nbr}${git_version_string}"
		sed -i "s/VERSION/${version}/" ${build_dir}/DEBIAN/control
		cat ${build_dir}/DEBIAN/control
		echo "version: ${version}"

		### create changelog
		pkg_name=$(grep -oP '(?<=Package: ).*' ${build_dir}/DEBIAN/control)
		mkdir -p ${build_dir}/usr/share/doc/${pkg_name}
		cat << EOF > ${build_dir}/usr/share/doc/${pkg_name}/changelog.Debian
${pkg_name} (${version}) ${distribution}; urgency=high

  * commit: ${git_commit_hash}

 -- $(grep -oP '(?<=Maintainer: ).*' ${build_dir}/DEBIAN/control)  $(date +'%a, %d %b %Y %H:%M:%S %z')

EOF
		gzip ${build_dir}/usr/share/doc/${pkg_name}/changelog.Debian

		### create debian package
		debfilename=${pkg_name}_${version}_${arch}.deb
		echo "${debfilename}"
		fakeroot dpkg-deb --build ${build_dir} $mod_dir/${debfilename}
	fi
fi

rm -rf ${build_dir}
echo "Done"
