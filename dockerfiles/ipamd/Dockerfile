FROM uhub.service.ucloud.cn/wxyz/cni-vpc-base:1.19.4 AS builder

WORKDIR /go/src/cni-vpc-uk8s

# Configure build with Go modules
ENV GO111MODULE=on
ENV GOPROXY=direct

COPY . ./
RUN make build-ipamd
RUN make build-cni

FROM centos:7.7.1908

RUN mkdir -p /app/
RUN mkdir -p /opt/cni/bin/
RUN mkdir -p /opt/cni/net.d/

COPY --from=builder /go/src/cni-vpc-uk8s/bin/cnivpc-ipamd /usr/local/bin/
COPY --from=builder /go/src/cni-vpc-uk8s/bin/cnivpc /app/
COPY config/10-cnivpc.conf /app/10-cnivpc.conf

ENTRYPOINT [ "/usr/local/bin/cnivpc-ipamd" ]
