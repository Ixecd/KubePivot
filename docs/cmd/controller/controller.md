# kp controller

全局 KubePivot Controller 的生命周期管理。

## 用法

```
kp controller <子命令>
```

## 子命令

### install

安装全局 Controller 到集群 `kubepivot-system` namespace。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--namespace` | `kubepivot-system` | controller 部署 namespace |
| `--image` | `qingchun22/kubepivot-controller:<kpVersion>` | controller 镜像 |
| `--kubeconfig` | | kubeconfig 路径 |
| `--context` | | kube context |
| `--wait` | `true` | 等待 Deployment ready |
| `--wait-timeout` | `120s` | 等待超时 |

**部署内容**：Namespace + ServiceAccount + ClusterRole + ClusterRoleBinding + ConfigMap + Deployment（3 副本）。

### uninstall

卸载 Controller。**危险操作**，会删除整个 `kubepivot-system` namespace 及所有 ClusterRole/Binding。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--namespace` | `kubepivot-system` | controller 部署 namespace |
| `--kubeconfig` | | kubeconfig 路径 |
| `--force` | `false` | 跳过确认 |

**不会动被管理项目的 namespace label**（用户需手工 `unenroll`）。

### status

查看 controller 状态。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--namespace` | `kubepivot-system` | controller 部署 namespace |
| `--kubeconfig` | | kubeconfig 路径 |

### enroll

将当前项目接入全局 controller（Namespace 打 `kubepivot.io/managed=true` label）。

### unenroll

取消接入。

### projects

列出所有被管理的项目。

## RBAC

- `install` → `PermControllerInstall`
- `uninstall` → `PermControllerUninstall`

## 分片机制

默认 3 副本 × 10 分片。每个 pod 最多持有 `ceil(10/3) = 4` 个分片 lease。hash(namespace) % N 决定项目归属哪个 shard。

## 相关命令

- `kp controller start` — pod 内部启动 Reconciliation Loop（CLI 不直接使用）
