FROM uhub.service.ucloud.cn/library/alpine:latest

COPY --chmod=0755 bin/cnivpc /app/cnivpc
COPY --chmod=0755 bin/cnivpc-init /usr/local/bin/cnivpc-init

ENTRYPOINT ["/usr/local/bin/cnivpc-init"]
