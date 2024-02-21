FROM alpine:latest

COPY bin/cnivpc /usr/local/bin/
COPY scripts/cnivpc-docker-entrypoint.sh /entrypoint.sh
COPY config/10-cnivpc.conf /10-cnivpc.conf

ENTRYPOINT [ "/entrypoint.sh" ]
