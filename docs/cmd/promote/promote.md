# kp promote

蓝绿流量切换。将流量从旧版本切换到新版本。

## 用法

```
kp promote [--service <name>] [--namespace <ns>]
```

## Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `--namespace` | (project.env) | K8s namespace |
| `--context` | (project.env) | kube context |
| `--kubeconfig` | (project.env) | kubeconfig 路径 |
| `--service` | (全部蓝绿服务) | 指定单个服务 |

## 工作流程

1. 读取 `configs/components.yaml`，过滤 `strategy: blue-green` 的服务
2. 查 etcd 蓝绿状态，找到 `InactiveSlot`
3. 切换 Service selector → 新 slot
4. 更新 etcd 状态

## RBAC

`PermPromote`

## 相关命令

- `kp sandbox start` — 部署 + 自动蓝绿切换
- `kp deploy --preview` — Header-based Preview 路由
