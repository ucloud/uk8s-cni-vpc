# RPS A/B 测试步骤

本文用于验证主机业务网卡开启 RPS（Receive Packet Steering）前后的 CPU 软中断分布、接收 PPS 和丢包率差异。一次 `RPS=0` 与 `RPS=ff` 的 A/B 对比即可；只有测试期间存在明显背景流量或结果波动时才需要重跑。

## 前置条件

- 准备两个 Kubernetes 节点：目标节点接收流量，客户端节点发送流量。
- 目标节点可通过 SSH 读取和写入 `/sys/class/net/eth1/queues/rx-0/rps_cpus`。
- 本地已配置 `kubectl`、`ssh` 和 `jq`。
- 测试镜像可被集群拉取：`uhub.service.ucloud.cn/andrew/rps-test:iperf3-20260713`。
- 以下示例适用于 8 个在线 CPU，`ff` 表示 CPU 0-7。其他 CPU 数量需要替换为对应掩码。

CNI 以宿主机进程运行，自动配置逻辑使用 Go `runtime.NumCPU()` 获取当前进程可用的逻辑 CPU 数。支持的虚拟机 CPU 规格及 mask 被明确限定为 `1→1`、`2→3`、`4→f`、`8→ff`、`16→ffff`、`32→ffffffff`、`64→ffffffff,ffffffff`；其他 CPU 数量告警并跳过。取得 mask 后遍历目标 UNI 的所有 `queues/rx-*/rps_cpus`，先读取并规范化当前值，与期望 mask 不同时才写入；例如 `000000ff` 与 `ff` 视为等价。

先设置测试变量：

```bash
export TARGET_NODE=192.168.234.147
export TARGET_HOST=root@192.168.234.147
export CLIENT_NODE=192.168.234.117
export TEST_IMAGE=uhub.service.ucloud.cn/andrew/rps-test:iperf3-20260713
export RPS_FILE=/sys/class/net/eth1/queues/rx-0/rps_cpus
```

确认目标网卡、队列数和原始 RPS 值：

```bash
ssh "$TARGET_HOST" "nproc; ethtool -l eth1; cat $RPS_FILE"
```

## 启动测试 Pod

在目标节点启动 iperf3 服务端 Pod：

```bash
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: rps-test-server
  labels:
    app: rps-test
spec:
  nodeName: ${TARGET_NODE}
  restartPolicy: Never
  containers:
  - name: iperf3
    image: ${TEST_IMAGE}
    imagePullPolicy: Always
    args: ["-s"]
EOF
```

在另一个节点启动使用主机网络的客户端 Pod。`tolerations` 允许把客户端放到控制平面节点：

```bash
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: rps-test-client
  labels:
    app: rps-test
spec:
  nodeName: ${CLIENT_NODE}
  hostNetwork: true
  tolerations:
  - operator: Exists
  restartPolicy: Never
  containers:
  - name: tools
    image: ${TEST_IMAGE}
    imagePullPolicy: Always
    command: ["/bin/sh", "-c"]
    args: ["sleep 3600"]
EOF

kubectl wait --for=condition=Ready pod/rps-test-server pod/rps-test-client --timeout=120s
export SERVER_IP=$(kubectl get pod rps-test-server -o jsonpath='{.status.podIP}')
```

确认测试流量确实进入目标节点的 `eth1`：

```bash
before=$(ssh "$TARGET_HOST" 'cat /sys/class/net/eth1/statistics/rx_packets')
kubectl exec rps-test-client -- iperf3 -c "$SERVER_IP" -P 2 -t 2
after=$(ssh "$TARGET_HOST" 'cat /sys/class/net/eth1/statistics/rx_packets')
echo "eth1 rx_packets delta: $((after-before))"
```

只有增量明显大于零时才继续 A/B 测试。

## 准备监控

node-exporter 需要启用 `softirqs` collector：

```bash
kubectl -n uk8s-monitor get ds uk8s-monitor-prometheus-node-exporter -o json \
  | jq -r '.spec.template.spec.containers[] | select(.name=="node-exporter") | .args[]' \
  | grep -- --collector.softirqs
```

若没有输出，应通过监控 Helm values 给 node-exporter 增加 `--collector.softirqs`。临时验证也可以直接修改 DaemonSet，修改后等待滚动更新完成：

```bash
kubectl -n uk8s-monitor patch ds uk8s-monitor-prometheus-node-exporter \
  --type=json \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--collector.softirqs"}]'

kubectl -n uk8s-monitor rollout status ds/uk8s-monitor-prometheus-node-exporter
```

直接修改 Helm 管理的 DaemonSet 可能在后续 Helm 升级时被覆盖，正式环境应同步到 Helm values。

## 执行一次 A/B 测试

先保存原始值，并注册退出时的恢复动作：

```bash
export ORIGINAL_RPS=$(ssh "$TARGET_HOST" "cat $RPS_FILE")
restore_rps() {
  ssh "$TARGET_HOST" "printf '%s' '$ORIGINAL_RPS' > $RPS_FILE"
}
trap restore_rps EXIT
```

关闭 RPS，执行 75 秒基线测试：

```bash
ssh "$TARGET_HOST" "printf 0 > $RPS_FILE"

kubectl exec rps-test-client -- \
  iperf3 -c "$SERVER_IP" -u -b 25M -l 64 -P 8 -t 75 --json \
  > /tmp/rps-off.json

jq '.end.sum | {bits_per_second, packets, lost_packets, lost_percent, jitter_ms}' \
  /tmp/rps-off.json
```

`-b 25M -P 8` 表示 8 个并行 UDP 流，每流 25 Mbit/s，总目标速率约 200 Mbit/s；64 字节负载对应约 39 万包/s。等待至少一个 Prometheus 抓取间隔后再执行下一阶段：

```bash
sleep 35
```

开启 CPU 0-7 的 RPS，使用完全相同的参数再测试一次：

```bash
ssh "$TARGET_HOST" "printf ff > $RPS_FILE"

kubectl exec rps-test-client -- \
  iperf3 -c "$SERVER_IP" -u -b 25M -l 64 -P 8 -t 75 --json \
  > /tmp/rps-on.json

jq '.end.sum | {bits_per_second, packets, lost_packets, lost_percent, jitter_ms}' \
  /tmp/rps-on.json
```

测试结束后立即恢复原始值：

```bash
restore_rps
trap - EXIT
ssh "$TARGET_HOST" "cat $RPS_FILE"
```

## 观察指标

查看每个 CPU 的 `NET_RX` 软中断速率：

```promql
rate(node_softirqs_functions_total{
  instance="192.168.234.147:9100",
  type="NET_RX",
  cpu=~"[0-7]"
}[1m])
```

查看每个 CPU 实际处理的网络包数：

```promql
rate(node_softnet_processed_total{
  instance="192.168.234.147:9100",
  cpu=~"[0-7]"
}[1m])
```

查看每个 CPU 的软中断占用：

```promql
rate(node_cpu_seconds_total{
  instance="192.168.234.147:9100",
  mode="softirq",
  cpu=~"[0-7]"
}[1m]) * 100
```

查看 `eth1` 接收 PPS：

```promql
rate(node_network_receive_packets_total{
  instance="192.168.234.147:9100",
  device="eth1"
}[1m])
```

预期关闭 RPS 时，网络包处理和 `%soft` 主要集中在网卡 IRQ 所在 CPU；开启后处理应分散到多个 CPU，同时 `eth1` 接收 PPS 上升、iperf3 丢包率下降。

## 清理

```bash
restore_rps
kubectl delete pod rps-test-client rps-test-server --ignore-not-found
```

最后再次确认目标网卡已恢复：

```bash
ssh "$TARGET_HOST" "cat $RPS_FILE"
```
