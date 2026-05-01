# kp pvc

PVC 快照备份和恢复。需要 CSI VolumeSnapshot 支持。

## 用法

```
kp pvc <子命令>
```

**前置**：集群已安装 VolumeSnapshot CRD + 存储类支持 CSI snapshot + 至少一个 VolumeSnapshotClass。

## 子命令

### backup

创建 PVC 快照。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--service` | (必填) | StatefulSet 服务名 |
| `--pvc` | (全部) | 指定备份单个 PVC |
| `--snapshot-class` | (集群默认) | VolumeSnapshotClass |
| `--timeout` | `5m` | 等待 Snapshot ready 超时 |
| `--namespace` | (project.env) | K8s namespace |
| `--context` | | kube context |
| `--kubeconfig` | | kubeconfig 路径 |

### restore

从快照恢复 PVC。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--service` | (必填) | StatefulSet 服务名 |
| `--snapshot` | (最近) | 指定快照名 |
| `--force` | `false` | 跳过确认 |
| `--namespace` | (project.env) | K8s namespace |
| `--context` | | kube context |
| `--kubeconfig` | | kubeconfig 路径 |

### list

列出快照。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--service` | (必填) | StatefulSet 服务名 |
| `--all` | `false` | 列出全部 |

## RBAC

`PermPVC`（backup / restore）。list 是只读，不加 RBAC。

## 相关命令

- `kp sandbox start` — 自动调 PVC backup 作为快照阶段
- `kp doctor` — 检查 CSI 支持
