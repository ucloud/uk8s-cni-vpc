# The building args, they will be injected into binary file.
CNI_VERSION=1.0.1-beta1
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

DOCKER_BASE_IMAGE:=$(if $(DOCKER_BASE_IMAGE),$(DOCKER_BASE_IMAGE),uhub.service.ucloud.cn/wxyz/cni-vpc-base:1.19.4)

.PHONY: docker-build docker-deploy docker-build-cni docker-base-image deploy-docker-base-image \
		check-fmt fmt install-check check version clean generate-grpc \
		build build-cni build-ipamd build-ipamctl build-vip-controller \
		install-grpc generate-k8s generate-grpc

all: build

build: build-cni build-ipamd build-ipamctl build-vip-controller

build-cni:
	go build ${LDFLAGS} -o ./bin/cnivpc ./cmd/cnivpc

build-ipamd:
	go build ${LDFLAGS} -o ./bin/cnivpc-ipamd ./cmd/cnivpc-ipamd

build-ipamctl:
	go build ${LDFLAGS} -o ./bin/ipamctl ./cmd/ipamctl

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

# Build cnivpc binary in image, so that the glibc can match the production
# environment.
# If you build cnivpc binary in the latest Linux distribution (Such as Arch),
# the binary might not be able to run in UK8S machine because the glibc version
# in UK8S machine is very old.
# we should use the image "uhub.service.ucloud.cn/wxyz/cni-vpc-base", it is based
# on centos 7 (same as production), and glibc version is properly.
docker-build-cni:
	${DOCKER_CMD} build -t ${CNI_VPC_BUILD_IMAGE} -f dockerfiles/cnivpc-build/Dockerfile .
	@mkdir -p bin
	@bash ./scripts/copy-from-docker-image.sh "${DOCKER_CMD}" "${CNI_VPC_BUILD_IMAGE}" /cnivpc ./bin/cnivpc
	@bash ./scripts/copy-from-docker-image.sh "${DOCKER_CMD}" "${CNI_VPC_BUILD_IMAGE}" /ipamctl ./bin/ipamctl
ifdef NODE_IP
	scp bin/docker/cnivpc root@${NODE_IP}:/opt/cni/bin/cnivpc
endif

docker-build-base-image:
	${DOCKER_CMD} build -t ${DOCKER_BASE_IMAGE} -f dockerfiles/base/Dockerfile .
	@echo "Successfully built ${DOCKER_BASE_IMAGE}"

docker-deploy-base-image: docker-base-image
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

install-check:
	@go install github.com/fzipp/gocyclo/cmd/gocyclo@latest
	@go install github.com/client9/misspell/cmd/misspell@latest
	@go install github.com/gordonklaus/ineffassign@latest
	@go install golang.org/x/tools/cmd/goimports@latest

check:
	@echo "==> check ineffassign"
	@ineffassign ./...
	@echo "==> check spell"
	@find . -type f -name '*.go' | xargs misspell -error
	@echo "==> check gocyclo"
	@gocyclo -over 70 .
	@echo "==> go vet"
	@go vet ./...

version:
	@echo ${CNI_VERSION}

clean:
	@rm -rf ./bin

install-grpc:
	@go install github.com/golang/protobuf/protoc-gen-go@latest

generate-grpc:
	@command -v protoc >/dev/null || { echo "ERROR: protoc not installed"; exit 1; }
	@command -v protoc-gen-go >/dev/null || { echo "ERROR: protoc-gen-go not installed"; exit 1; }
	@protoc --go_out=plugins=grpc:./rpc ./rpc/ipamd.proto

generate-k8s:
	@bash hack/update-codegen.sh
