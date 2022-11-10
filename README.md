# UCloud K8s CNI-VPC Plugin

This a [CNI plugin](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/network-plugins/) to implement underlay container network based on [UCloud VPC 2.0](https://docs.ucloud.cn/vpc/README).

The plugin has 3 components:

- CNI-VPC, setup container networking, including `veth`, `route`, `arp`, etc. It will be called by `kubelet`.
- IPAMD, optional, IP Address Manager Daemon, responsible for allocating and reclaiming IP addresses. Can speed up Pod creation and destruction. If you want to use [static IP](https://docs.ucloud.cn/uk8s/network/static_ip), this component must be deployed.
- VIP-Controller, optional, an operator to implement static IP assignment, works with IPAMD.

You can find our design documentation and implementation details [here](doc).

## Install

Download the CNI binary file from the release page, add it to `/opt/cni/bin`, for example, the binary path might be `/opt/cni/bin/cnivpc`.

The config file is stored in `/opt/cni/net.d`, you need to add config file [10-cnivpc.conf](config/10.cnivpc.conf) to this path, for example, the config path might be `/opt/cni/net.d/10-cnivpc.conf`.

If you have only one CNI to use, the binary and config filename can be casual. But if you have multiple CNIs, the config filename should end with binary filename before the ext. For example, `10-cnivpc.conf` ends with `cnivpc`. So that kubelet can find matching config file for different CNIs.

Then, launch kubelet with network plugins set `--network-plugin=cni`. Setting `--max-pods` will prevent scheduling that exceeds the IP address resources available to the kubelet.

The CNI can only be run on [UCloud UHost Machine](https://docs.ucloud.cn/uhost/README) to have access to [UCloud API](https://docs.ucloud.cn/api) to allocate VPC IP Adresses.

The [IPAMD](doc/ipamd.md) is an optional component to speed up Pod creation and destruction. We strongly recommend that you deploy this component:

```shell
kubectl apply -f https://raw.githubusercontent.com/ucloud/uk8s-cni-vpc/deploy/ipamd.yaml
```

If you want to use static IP, you can read the instruction [here](https://docs.ucloud.cn/uk8s/network/static_ip).

## Build

If you are using a CentOS Linux, you can build the cni binary directly:

```shell
make build-cni
```

The cni binary file will be saved to `./bin/cnivpc`.

If you are not using CentOS, like MacOS or other Linux distribution (such as ArchLinux), you need to use Docker to build the cni binary, because the cni requires some special C libraries in CentOS. So we need to use a [special centos docker image](./dockerfiles/centos-go/Dockerfile) to build it:

```shell
make docker-build-cni
```

**Be attention that the cni binary built on none-CentOS Linux might not be able to run in UK8S machine.**

The cni binary file will be saved to `./bin/docker/cnivpc`.

You can use the following command to build docker images for IPAMD and VIP-Controller:

```shell
make build-image
```
