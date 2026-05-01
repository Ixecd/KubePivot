# kp down

彻底下线服务。删除 namespace 内所有 K8s 资源。

## 用法

```
kp down [flags]
```

## Flag

| Flag | 默认值 | 说明 |
|---|---|---|
| `--namespace` | (project.env) | K8s namespace |
| `--context` | (project.env) | kube context |
| `--kubeconfig` | (project.env) | kubeconfig 路径 |

## RBAC

`PermRollback`（销毁性操作，与 rollback 同级）

## 相关命令

- `kp rollback` — 回滚（保留 Pod）
- `kp deploy` — 部署
