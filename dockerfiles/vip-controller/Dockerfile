FROM uhub.service.ucloud.cn/wxyz/cni-vpc-base:1.20.4 AS builder

WORKDIR /go/src/cni-vpc-uk8s

# Configure build with Go modules
ENV GO111MODULE=on
ENV GOPROXY=direct

COPY . ./
RUN make build-vip-controller

FROM centos:7.7.1908

COPY --from=builder /go/src/cni-vpc-uk8s/bin/vip-controller /usr/local/bin/

ENTRYPOINT [ "/usr/local/bin/vip-controller" ]
