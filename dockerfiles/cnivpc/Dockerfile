FROM uhub.service.ucloud.cn/wxyz/cni-vpc-base:1.19.4 AS builder

WORKDIR /go/src/cni-vpc-uk8s

# Configure build with Go modules
ENV GO111MODULE=on
ENV GOPROXY=direct

COPY . ./
RUN make build-cni

FROM uhub.service.ucloud.cn/wxyz/cnivpc-plugin-basic:1.0

RUN mkdir -p /app
RUN mkdir -p /app/cni/bin
RUN mkdir -p /app/cni/net.d

COPY --from=builder /go/src/cni-vpc-uk8s/bin/cnivpc /app/cni/bin/cnivpc
COPY config/10-cnivpc.conf /app/cni/net.d

ENTRYPOINT [ "" ]
