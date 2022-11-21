# The building args, they will be injected into binary file.
CNI_VERSION=0.1.0
WORKDIR=$(shell pwd)
PKG_VERSION_PATH="github.com/ucloud/uk8s-cni-vpc/pkg/version"
GO_VERSION=$(shell go version)
BUILD_TIME=$(shell date +%F-%Z/%T)
COMMIT_ID=$(shell git rev-parse HEAD)
COMMIT_ID_SHORT=$(shell git rev-parse --short HEAD)
LDFLAGS= -ldflags  "-X '${PKG_VERSION_PATH}.CNIVersion=${CNI_VERSION}' -X ${PKG_VERSION_PATH}.BuildTime=${BUILD_TIME} -X ${PKG_VERSION_PATH}.ProgramCommitID=${COMMIT_ID}"

# Go args, the cni-vpc only support Linux os.
export GOOS=linux
export GO111MODULE=on
export GOARCH=$(TARGETARCH)

DOCKER_DEPLOY_BUCKET=uhub.service.ucloud.cn/uk8s
DOCKER_TEST_BUCKET=uhub.service.ucloud.cn/wxyz

DOCKER_LABEL:=$(if $(DEPLOY),$(CNI_VERSION),build-$(COMMIT_ID_SHORT))
DOCKER_BUCKET:=$(if $(DEPLOY),$(DOCKER_DEPLOY_BUCKET),$(DOCKER_TEST_BUCKET))

IPAMD_IMAGE:=$(DOCKER_BUCKET)/cni-vpc-ipamd:$(DOCKER_LABEL)
VIP_CONTROLLER_IMAGE:=$(DOCKER_BUCKET)/vip-controller:$(DOCKER_LABEL)
CNI_VPC_BUILD_IMAGE:=$(DOCKER_BUCKET)/cni-vpc-build:$(DOCKER_LABEL)

DOCKER_CMD:=$(if $(DOCKER_CMD),$(DOCKER_CMD),docker)

DOCKER_BASE_IMAGE:=$(if $(DOCKER_BASE_IMAGE),$(DOCKER_BASE_IMAGE),uhub.service.ucloud.cn/wxyz/centos-go:1.19.2)

.PHONY: docker-build docker-deploy docker-build-cni docker-base-image deploy-docker-base-image \
		check-fmt fmt version clean generate-grpc \
		build build-cni build-ipamd build-vip-controller

all: build

build: build-cni build-ipamd build-vip-controller

build-cni:
	go build ${LDFLAGS} -o ./bin/cnivpc ./cmd/cnivpc

build-ipamd:
	go build ${LDFLAGS} -o ./bin/cnivpc-ipamd ./cmd/cnivpc-ipamd

build-vip-controller:
	go build ${LDFLAGS} -o ./bin/vip-controller ./cmd/vip-controller

docker-build:
	${DOCKER_CMD} build -t ${IPAMD_IMAGE} -f dockerfiles/ipamd/Dockerfile .
	${DOCKER_CMD} build -t ${VIP_CONTROLLER_IMAGE} -f dockerfiles/vip-controller/Dockerfile .
	@echo "Successfully built image: ${IPAMD_IMAGE}"
	@echo "Successfully built image: ${VIP_CONTROLLER_IMAGE}"

docker-deploy: docker-build
	${DOCKER_CMD} push ${IPAMD_IMAGE}
	${DOCKER_CMD} push ${VIP_CONTROLLER_IMAGE}
	@echo "Successfully pushed image: ${IPAMD_IMAGE}"
	@echo "Successfully pushed image: ${VIP_CONTROLLER_IMAGE}"

# NOTE: Due to some C lib reasons, the cni binary built by some Linux
# distributions (suck as ArchLinux) cannot run in CentOS, so it is
# necessary to build the cni binary from docker and copy the artifact to
# the host.
docker-build-cni:
	${DOCKER_CMD} build -t ${CNI_VPC_BUILD_IMAGE} -f dockerfiles/cnivpc-build/Dockerfile .
	@mkdir -p bin
	@bash ./scripts/copy-from-docker-image.sh "${DOCKER_CMD}" "${CNI_VPC_BUILD_IMAGE}" /cnivpc ./bin/cnivpc
ifdef NODE_IP
	scp bin/docker/cnivpc root@${NODE_IP}:/opt/cni/bin/cnivpc
endif

docker-base-image:
	${DOCKER_CMD} build -t ${DOCKER_BASE_IMAGE} -f dockerfiles/centos-go/Dockerfile .
	@echo "Successfully built ${DOCKER_BASE_IMAGE}"

deploy-docker-base-image: docker-base-image
	${DOCKER_CMD} push ${DOCKER_BASE_IMAGE}
	@echo "Successfully pushed ${DOCKER_BASE_IMAGE}"

fmt:
	@command -v goimports >/dev/null || { echo "ERROR: goimports not installed"; exit 1; }
	@exit $(shell find ./* \
	  -type f \
	  -name '*.go' \
	  -print0 | sort -z | xargs -0 -- goimports $(or $(FORMAT_FLAGS),-w) | wc -l | bc)

check-fmt:
	@./scripts/check-fmt.sh

version:
	@echo ${CNI_VERSION}

clean:
	@rm -rf ./bin

generate-grpc:
	@command -v protoc >/dev/null || { echo "ERROR: protoc not installed"; exit 1; }
	@command -v protoc-gen-go >/dev/null || { echo "ERROR: protoc-gen-go not installed"; exit 1; }
	@protoc --go_out=plugins=grpc:./pkg/rpc ./pkg/rpc/ipamd.proto
