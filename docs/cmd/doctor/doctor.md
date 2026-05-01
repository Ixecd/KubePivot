# kp doctor

环境依赖检查。检测 KubePivot 运行所需的外部工具和集群能力。

## 用法

```
kp doctor [flags]
```

## 子检查项

| 检查 | 说明 |
|---|---|
| 基础工具 | docker / kubectl / helm |
| 安全工具 | cosign / syft / kubeseal / trivy |
| 性能 (`--perf`) | 集群资源 / 网络延迟 |
| 跨 NS (`--cross-ns`) | 跨 namespace 通信检查 |
| etcd (`--etcd`) | etcd 连接检查 |
| PVC (`--pvc`) | CSI VolumeSnapshot 支持检查 |
| Secret (`--secret`) | Secret 管理检查 |
| 安全基线 (`--security`) | RBAC / PSP / 网络策略检查 |

## 相关命令

- `kp supply-chain verify` — cosign 依赖检测复用 doctor 模式
- `kp secret seal` — kubeseal 依赖检测复用 doctor 模式
