FROM uhub.service.ucloud.cn/wxyz/centos-go:1.19.2 AS builder

WORKDIR /go/src/cni-vpc-uk8s

# Configure build with Go modules
ENV GO111MODULE=on
ENV GOPROXY=direct

COPY . ./
RUN make build-cni

FROM scratch AS export
COPY --from=builder /go/src/cni-vpc-uk8s/bin/cnivpc /
ENTRYPOINT [ "bash" ]
