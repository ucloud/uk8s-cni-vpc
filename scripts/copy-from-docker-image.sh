#!/bin/bash

set -e

DOCKER_CMD=$1
IMAGE=$2
SRC_PATH=$3
TAR_PATH=$4

TMP_ID=`${DOCKER_CMD} create ${IMAGE} bash`

echo "temp id is ${TMP_ID}"
echo "from ${SRC_PATH} to ${TAR_PATH}"
${DOCKER_CMD} cp ${TMP_ID}:${SRC_PATH} ${TAR_PATH}
${DOCKER_CMD} rm -v ${TMP_ID}
