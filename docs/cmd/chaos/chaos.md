# kp chaos

混沌工程实验管理。基于 Chaos Mesh API。

## 用法

```
kp chaos <子命令>
```

**前置**：Chaos Mesh 已安装，port-forward 到 `127.0.0.1:2333`。

## 子命令

### inject

注入混沌实验。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--service` | (必填) | 目标服务名 |
| `--kind` | `pod-kill` | 混沌类型：`pod-kill` / `network-delay` / `cpu-stress` / `memory-stress` |
| `--namespace` | (project.env → default) | K8s namespace |
| `--duration` | `30s` | 持续时间 |
| `--chaos-mesh` | `http://127.0.0.1:2333` | Chaos Mesh API 地址 |
| `--dry-run` | `false` | 只打印配置 |
| `--latency` | `100ms` | 网络延迟（network-delay 类型） |
| `--workers` | `1` | 压测线程数（cpu-stress / memory-stress） |
| `--context` | | kube context |

### list

列出当前混沌实验。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--chaos-mesh` | `http://127.0.0.1:2333` | Chaos Mesh API 地址 |
| `--namespace` | (project.env → default) | 过滤 namespace |

### stop

停止混沌实验。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--uid` | (必填) | 实验 UID |
| `--namespace` | (project.env → default) | K8s namespace |
| `--chaos-mesh` | `http://127.0.0.1:2333` | Chaos Mesh API 地址 |

### status

查看实验状态。

| Flag | 默认值 | 说明 |
|---|---|---|
| `--uid` | (必填) | 实验 UID |
| `--chaos-mesh` | `http://127.0.0.1:2333` | Chaos Mesh API 地址 |

## RBAC

`PermChaos`（inject / stop）。list / status 是只读，不加 RBAC。

## 相关命令

- `kp doctor` — 环境检查
