FROM uhub.service.ucloud.cn/wxyz/cni-vpc-base:1.20.4 AS builder

WORKDIR /go/src/cni-vpc-uk8s

# Configure build with Go modules
ENV GO111MODULE=on
ENV GOPROXY=direct

COPY . ./
RUN make build-cni
RUN make build-ipamctl

FROM scratch AS export
COPY --from=builder /go/src/cni-vpc-uk8s/bin/cnivpc /
COPY --from=builder /go/src/cni-vpc-uk8s/bin/ipamctl /
ENTRYPOINT [ "bash" ]
