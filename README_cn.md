[English](README.md) | 简体中文

<h1 align="center">UCloud K8s CNI-VPC Plugin</h1>

这是[UK8S](https://docs.ucloud.cn/uk8s/userguide/before_start)中的underlay容器网络插件源代码，基于[UCloud VPC 2.0](https://docs.ucloud.cn/vpc/README)。所有UK8S集群的容器网络都依靠这个插件进行配置。

插件包含3个组件：

- [CNI-VPC](doc/cni.md)：底层网络插件，用于设置或销毁Pod网络。
- [IPAMD](doc/ipamd.md)：可选组件，IP地址管理器。复用IP地址以提高Pod创建和销毁的速度。如果你的集群规模较大，建议安装这个组件。如果你要使用[静态IP]()，则该组件必须安装。
- VIP-Controller：可选组建，用于管理静态IP。在使用静态IP时需要部署。

你可以在[这里](doc)找到我们的设计文档。

## 安装

CNI本身会被默认安装到UK8S集群中。无需你进行额外配置。你可以在UK8S控制台管理和升级CNI，详见[网络插件升级](https://docs.ucloud.cn/uk8s/network/cni_update)。

控制台只能将CNI升级到stable版本，如果你想使用历史版本，或体验rc版本，可以到我们的[release页面](https://github.com/ucloud/uk8s-cni-vpc/releases)下载CNI二进制文件，在机器中直接替换掉`/opt/cni/bin/cnivpc`即可。

CNI的配置文件保存在`/opt/cni/net.d/10-cnivpc.conf`下面，你可以在[这里](config/10-cnivpc.conf)找到配置默认值。

每次当Pod创建和销毁时，kubelet都会调用CNI进行网络设置，你可以在UK8S机器上面通过下面的命令查看CNI的日志：

```bash
tail -f /var/log/cnivpc.log
```

[IPAMD](doc/ipamd.md)可以通过复用IP来提高集群创建和销毁Pod的速度，如果你发现集群有大量Pod卡在Pendding状态等待网络设置，可以考虑部署IPAMD。IPAMD以DeamonSet的形式运行，可以随时安装和卸载。部署最新的IPAMD：

```bash
kubectl apply -f https://raw.githubusercontent.com/ucloud/uk8s-cni-vpc/main/deploy/ipamd.yaml
```

如果你希望使用固定IP，则需要部署额外的组件，可以参考[这篇文档](https://docs.ucloud.cn/uk8s/network/static_ip)。

## 构建





## 调试

