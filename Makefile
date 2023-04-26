# The building args, they will be injected into binary file.
WORKDIR=$(shell pwd)
PKG_VERSION_PATH="github.com/ucloud/uk8s-cni-vpc/pkg/version"
GO_VERSION=$(shell go version)
BUILD_TIME=$(shell date +%F-%Z/%T)
COMMIT_ID=$(shell git rev-parse HEAD)
COMMIT_ID_SHORT=$(shell git rev-parse --short HEAD)
LDFLAGS= -ldflags  "-X '${PKG_VERSION_PATH}.CNIVersion=${CNI_VERSION}' -X ${PKG_VERSION_PATH}.BuildTime=${BUILD_TIME} -X ${PKG_VERSION_PATH}.ProgramCommitID=${COMMIT_ID}"

# If current commit is tagged, use tag as version, else, use dev-${COMMIT_ID} as version
CNI_VERSION=$(shell git tag --points-at ${COMMIT_ID})
CNI_VERSION:=$(if $(CNI_VERSION),$(CNI_VERSION),dev-${COMMIT_ID_SHORT})
CNI_VERSION:=$(shell echo ${CNI_VERSION} | sed -e "s/^v//")

# Go args, the cni-vpc only support Linux os.
export GOOS=linux
export GO111MODULE=on
export GOARCH=$(TARGETARCH)

DOCKER_DEPLOY_BUCKET=uhub.service.ucloud.cn/uk8s
DOCKER_TEST_BUCKET=uhub.service.ucloud.cn/wxyz

DOCKER_LABEL:=$(if $(DEPLOY),$(CNI_VERSION),dev-$(COMMIT_ID_SHORT))
DOCKER_BUCKET:=$(if $(DEPLOY),$(DOCKER_DEPLOY_BUCKET),$(DOCKER_TEST_BUCKET))

IPAMD_IMAGE:=$(DOCKER_BUCKET)/cni-vpc-ipamd:$(DOCKER_LABEL)
VIP_CONTROLLER_IMAGE:=$(DOCKER_BUCKET)/vip-controller:$(DOCKER_LABEL)
CNI_VPC_BUILD_IMAGE:=$(DOCKER_BUCKET)/cni-vpc-build:$(DOCKER_LABEL)

DOCKER_CMD:=$(shell if docker ps 2> /dev/null; then echo "docker"; else echo "sudo docker"; fi)
CWD:=$(shell pwd)

DOCKER_BASE_IMAGE:=$(if $(DOCKER_BASE_IMAGE),$(DOCKER_BASE_IMAGE),uhub.service.ucloud.cn/wxyz/cni-vpc-base:1.19.6)

all: build-cni build-ipamd build-vip-controller

.PHONY: build-cni
build-cni:
	go build ${LDFLAGS} -o ./bin/cnivpc ./cmd/cnivpc

.PHONY: build-ipamd
build-ipamd:
	go build ${LDFLAGS} -o ./bin/cnivpc-ipamd ./cmd/cnivpc-ipamd

.PHONY: build-vip-controller
build-vip-controller:
	go build ${LDFLAGS} -o ./bin/vip-controller ./cmd/vip-controller

.PHONY: docker-build-bash
docker-build-bash:
	$(DOCKER_CMD) run -v $(CWD):/src -w="/src" -it $(DOCKER_BASE_IMAGE) bash

.PHONY: docker-build
docker-build:
	$(DOCKER_CMD) run -v $(CWD):/src -w="/src" -it $(DOCKER_BASE_IMAGE) make

.PHONY: docker-base
docker-base:
	$(DOCKER_CMD) build -t $(DOCKER_BASE_IMAGE) -f dockerfiles/base/Dockerfile .
	@echo "Successfully built ${DOCKER_BASE_IMAGE}"

.PHONY: docker-ipamd
docker-ipamd:
	$(DOCKER_CMD) build -t $(IPAMD_IMAGE) -f dockerfiles/ipamd/Dockerfile .
	@echo "Successfully built image: ${IPAMD_IMAGE}"

.PHONY: docker-vip-controller
docker-vip-controller:
	$(DOCKER_CMD) build -t $(VIP_CONTROLLER_IMAGE) -f dockerfiles/vip-controller/Dockerfile .
	@echo "Successfully built image: ${VIP_CONTROLLER_IMAGE}"

.PHONY: fmt
fmt:
	@command -v goimports >/dev/null || { echo "ERROR: goimports not installed"; exit 1; }
	@exit $(shell find ./* \
	  -type f \
	  -name '*.go' \
	  -print0 | sort -z | xargs -0 -- goimports $(or $(FORMAT_FLAGS),-w) | wc -l | bc)

.PHONY: check-fmt
check-fmt:
	@./scripts/check-fmt.sh

.PHONY: install-check
install-check:
	@go install github.com/fzipp/gocyclo/cmd/gocyclo@latest
	@go install github.com/client9/misspell/cmd/misspell@latest
	@go install github.com/gordonklaus/ineffassign@latest
	@go install golang.org/x/tools/cmd/goimports@latest

.PHONY: check
check:
	@echo "==> check ineffassign"
	@ineffassign ./...
	@echo "==> check spell"
	@find . -type f -name '*.go' | xargs misspell -error
	@echo "==> check gocyclo"
	@gocyclo -over 70 .
	@echo "==> go vet"
	@go vet ./...

.PHONY: version
version:
	@echo ${CNI_VERSION}

.PHONY: clean
clean:
	@rm -rf ./bin

.PHONY: install-grpc
install-grpc:
	@go install github.com/golang/protobuf/protoc-gen-go@latest

.PHONY: generate-grpc
generate-grpc:
	@command -v protoc >/dev/null || { echo "ERROR: protoc not installed"; exit 1; }
	@command -v protoc-gen-go >/dev/null || { echo "ERROR: protoc-gen-go not installed"; exit 1; }
	@protoc --go_out=plugins=grpc:./rpc ./rpc/ipamd.proto

.PHONY: generate-k8s
generate-k8s:
	@bash hack/update-codegen.sh
