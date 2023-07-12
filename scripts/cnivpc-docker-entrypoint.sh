#!/bin/sh

if [[ -d /opt/cni ]]; then
	cp -f /usr/local/bin/cnivpc /opt/cni/bin/cnivpc
	cp -f /10-cnivpc.conf /opt/cni/net.d/10-cnivpc.conf
fi

if [[ -d /host-bin ]]; then
	cp -f /usr/local/bin/cnivpctl /host-bin/cnivpctl
fi

while true; do
	if [[ ! -f "/var/log/cnivpc.log" ]]; then
		echo "waitting for cnivpc logs..."
		sleep 3
		continue
	fi
	tail -f /var/log/cnivpc.log
done
