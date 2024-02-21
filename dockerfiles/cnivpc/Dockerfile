FROM alpine:latest

COPY bin/cnivpc /usr/local/bin/
RUN touch /usr/local/bin/cnivpctl
COPY scripts/cnivpc-docker-entrypoint.sh /entrypoint.sh
COPY config/10-cnivpc.conf /10-cnivpc.conf

ENTRYPOINT [ "/entrypoint.sh" ]
