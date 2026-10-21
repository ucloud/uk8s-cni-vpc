FROM uhub.service.ucloud.cn/library/alpine:latest

RUN apk add --no-cache iptables iptables-legacy

COPY bin/cnivpc-ipamd /usr/local/bin/

ENTRYPOINT [ "/usr/local/bin/cnivpc-ipamd" ]
