#!/bin/bash

set -eu

VERSION=${GITHUB_REF#refs/*/}

if [ ! -f ./bin/cnivpc ]; then
	echo "cannot find cnivpc binary"
	exit 1
fi

cp LICENSE ./bin

ASSET_FILE="uk8s-cni-vpc_${VERSION}.zip"
zip -r ${ASSET_FILE} ./bin
echo "ASSET_FILE=${ASSET_FILE}" >> $GITHUB_ENV
